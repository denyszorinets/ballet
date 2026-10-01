"""Sphinx configuration for the Ballet documentation site (ADR-0001)."""

project = "Ballet"
author = "Ballet contributors"
copyright = "2026, Ballet contributors"

extensions = [
    "sphinx.ext.extlinks",
    "sphinx_design",
    "sphinxcontrib.mermaid",
]

root_doc = "index"
exclude_patterns = ["_build", ".venv", "Thumbs.db", ".DS_Store", "**/*.md"]

html_theme = "furo"
html_title = "Ballet"
html_static_path = ["_static"]

nitpicky = True

# :issue:`28` and :repo:`Makefile` roles.
_github = "https://github.com/denyszorinets/ballet"
extlinks = {
    "issue": (f"{_github}/issues/%s", "#%s"),
    "repo": (f"{_github}/blob/develop/%s", "%s"),
}

# The repository is private: anonymous link checks would get 404.
# Local development URLs exist only on a developer's machine.
linkcheck_ignore = [rf"{_github}/.*", r"https?://(localhost|127\.0\.0\.1)(:\d+)?(/.*)?"]
