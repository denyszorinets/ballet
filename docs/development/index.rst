Development
===========

Contributor workflow for Ballet itself.

.. toctree::
   :maxdepth: 1

   code-layout
   web

Workflow
--------

- Work is tracked as tickets; every branch is
  ``feature/<ticket>_<short_title>`` from ``develop``; changes reach
  ``develop`` through reviewed pull requests; ``main`` is the release
  branch.
- New behavior is developed test-first (Go, testify, table-driven tests).
- Documentation is updated in the same change as the behavior it
  describes.

Documentation
-------------

.. code-block:: bash

   make docs          # build into docs/_build/html
   make docs-serve    # live preview on http://127.0.0.1:8000
   make docs-check    # strict build + link check; must pass before commit
