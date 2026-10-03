#!/usr/bin/env python3
"""Onboards Ballet's own repository as a Ballet project (dogfooding).

Idempotent: run it again to update credentials, settings and skills.

  ANTHROPIC_API_KEY=… GITHUB_TOKEN=… scripts/dogfood-setup.py [--import-issues]

Environment:
  BALLET_URL          Core (default http://localhost:8080)
  BALLET_TOKEN        bearer token of an organization admin; default: the
                      development realm's alice (scripts/dev-token.sh)
  ANTHROPIC_API_KEY   key for the customer's agent sessions and planner
  GITHUB_TOKEN        token that can push branches and open pull requests
  BALLET_REPO         repository (default https://github.com/denyszorinets/ballet.git)
  ISSUES_REPO         owner/name whose issues --import-issues reads
                      (default: from BALLET_REPO)
  BALLET_AGENT_IMAGE  image of agent sessions with the Docker backend
                      (default ballet-dogfood, deploy/agent/ballet.Containerfile)
  BALLET_FORGE        github (default) or git (a repository without GitHub,
                      e.g. a local mirror for a rehearsal)
  ISSUES_TOKEN        token reading GitHub issues for --import-issues
                      (default GITHUB_TOKEN)
  TICKET_TOKENS, DAILY_TOKENS   budgets (default 3,000,000 and 30,000,000)

--import-issues creates a backlog ticket for every open GitHub issue
labelled "task" that has no ticket yet; move the ones for the night to
Ready.
"""
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
URL = os.environ.get("BALLET_URL", "http://localhost:8080").rstrip("/")
CUSTOMER, PROJECT = "ballet", "BAL"
REPO = os.environ.get("BALLET_REPO", "https://github.com/denyszorinets/ballet.git")


def token() -> str:
    if os.environ.get("BALLET_TOKEN"):
        return os.environ["BALLET_TOKEN"]
    return subprocess.check_output([str(ROOT / "scripts" / "dev-token.sh"), "alice"], text=True).strip()


TOKEN = token()


def api(method, path, body=None, ok=(200, 201, 204)):
    req = urllib.request.Request(URL + "/api/v1" + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req) as r:
            data = r.read()
            return r.status, json.loads(data) if data else None
    except urllib.error.HTTPError as e:
        data = e.read()
        if e.code in ok:
            return e.code, json.loads(data) if data else None
        return e.code, json.loads(data) if data else None


def must(method, path, body=None):
    code, out = api(method, path, body)
    if code not in (200, 201, 204):
        sys.exit(f"{method} {path}: {code} {out}")
    return out


def need(name):
    v = os.environ.get(name)
    if not v:
        sys.exit(f"{name} is required")
    return v


def step(msg):
    print("==>", msg)


def setup_tenancy():
    step(f"customer {CUSTOMER} and project {PROJECT}")
    if api("GET", f"/customers/{CUSTOMER}")[0] == 404:
        must("POST", "/customers", {"key": CUSTOMER, "name": "Ballet"})
    if api("GET", f"/projects/{PROJECT}")[0] == 404:
        must("POST", f"/customers/{CUSTOMER}/projects",
             {"key": PROJECT, "name": "Ballet", "description": "Ballet developing itself."})


def setup_credentials():
    step("credentials (Anthropic key, git token)")
    must("PUT", f"/customers/{CUSTOMER}/credentials/anthropic", {"api_key": need("ANTHROPIC_API_KEY")})
    must("PUT", f"/projects/{PROJECT}/credentials/git", {"api_key": need("GITHUB_TOKEN")})


def setup_execution():
    step("execution settings")
    ex = must("GET", f"/projects/{PROJECT}/execution")
    must("PUT", f"/projects/{PROJECT}/execution", {
        "repo_url": REPO, "default_branch": "develop",
        "image": os.environ.get("BALLET_AGENT_IMAGE", "ballet-dogfood"),
        "branch_template": "feature/{ticket}_{slug}",
        "git_name": "Ballet", "git_email": "ballet@users.noreply.github.com",
        "forge": os.environ.get("BALLET_FORGE", "github"), "version": ex["version"]})


def frontmatter(text):
    m = re.match(r"^---\n(.*?)\n---\n(.*)$", text, re.S)
    if not m:
        return {}, text
    meta = {}
    for line in m.group(1).splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            meta[k.strip()] = v.strip().strip('"')
    return meta, m.group(2).lstrip("\n")


def setup_skills():
    step("project skills from .claude/skills")
    _, existing = api("GET", f"/skills?scope=project:{PROJECT}")
    by_name = {s["name"]: s for s in (existing or {}).get("items", [])}
    for d in sorted((ROOT / ".claude" / "skills").iterdir()):
        main = d / "SKILL.md"
        if not main.is_file():
            continue
        meta, body = frontmatter(main.read_text())
        name = meta.get("name", d.name)
        files = {str(p.relative_to(d)): p.read_text() for p in d.rglob("*")
                 if p.is_file() and p != main and p.suffix in {".md", ".txt", ".sh", ".py", ".yaml", ".json"}}
        desc = meta.get("description", name)[:500]
        if name in by_name:
            s = must("GET", f"/skills/{by_name[name]['id']}")
            if s["latest_version"] > 0 and (s["description"], s["body"], s["files"]) == (desc, body, files):
                print(f"    {name}: unchanged")
                continue
            s = must("PATCH", f"/skills/{s['id']}", {"version": s["version"], "description": desc, "body": body,
                                                   "files": files})
        else:
            s = must("POST", "/skills", {"scope": f"project:{PROJECT}", "name": name, "description": desc,
                                         "body": body, "files": files})
        code, out = api("POST", f"/skills/{s['id']}/publish", {"version": s["version"]})
        print(f"    {name}: {'published' if code in (200, 201) else 'unchanged'}")


def setup_budget():
    step("budget")
    b = must("GET", f"/projects/{PROJECT}/budget")
    must("PUT", f"/projects/{PROJECT}/budget", {"ticket_tokens": int(os.environ.get("TICKET_TOKENS", 3_000_000)),
                                                 "daily_tokens": int(os.environ.get("DAILY_TOKENS", 30_000_000)),
                                                 "version": b["version"]})


def import_issues():
    step("tickets from open GitHub task issues")
    owner_repo = os.environ.get("ISSUES_REPO") or re.sub(r"(\.git)?$", "", REPO.split("github.com/")[-1])
    req = urllib.request.Request(f"https://api.github.com/repos/{owner_repo}/issues?state=open&labels=task&per_page=100",
                                 headers={"Authorization": "Bearer " + (os.environ.get("ISSUES_TOKEN")
                                                                        or need("GITHUB_TOKEN")),
                                          "Accept": "application/vnd.github+json"})
    with urllib.request.urlopen(req) as r:
        issues = [i for i in json.load(r) if "pull_request" not in i]
    _, items = api("GET", f"/projects/{PROJECT}/items?kind=ticket")
    titles = {it["title"] for it in (items or {}).get("items", [])}
    for i in issues:
        t = ticket_for(i)
        if t["title"] in titles:
            continue
        must("POST", f"/projects/{PROJECT}/items", t)
        print(f"    {t['title']}")


def ticket_for(issue):
    """A backlog ticket for a GitHub issue; its acceptance criteria come
    from the issue's "## Acceptance Criteria" list."""
    body = issue.get("body") or ""
    criteria = []
    if "## Acceptance Criteria" in body:
        section = body.split("## Acceptance Criteria", 1)[1].split("\n## ", 1)[0]
        criteria = re.findall(r"^\s*[-*]\s+(?:\[[ x]\]\s+)?(.+?)\s*$", section, re.M)
    return {"kind": "ticket", "title": f"#{issue['number']} {issue['title']}"[:200],
            "description": body + f"\n\nGitHub issue: {issue['html_url']}",
            "acceptance_criteria": criteria or [issue["title"]]}


def main():
    setup_tenancy()
    setup_credentials()
    setup_execution()
    setup_skills()
    setup_budget()
    if "--import-issues" in sys.argv:
        import_issues()
    print(f"\nDone. Open {URL}/projects/{PROJECT}, move tickets to Ready (or plan them with the planner),\n"
          f"and read the digest in the morning: {URL}/projects/{PROJECT}/digest")


if __name__ == "__main__":
    main()
