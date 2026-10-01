"""Sphinx configuration for the Ballet documentation site (ADR-0001)."""

project = "Ballet"
author = "Ballet contributors"
copyright = "2026, Ballet contributors"

extensions = [
    "sphinx_design",
    "sphinxcontrib.mermaid",
]

root_doc = "index"
exclude_patterns = ["_build", ".venv", "Thumbs.db", ".DS_Store", "**/*.md"]

html_theme = "furo"
html_title = "Ballet"
html_static_path = ["_static"]

nitpicky = True
