Website and documentation publishing
====================================

Ballet's public site is published on GitHub Pages at
``https://denyszorinets.github.io/ballet/``:

``/``
   The landing page, a static page in :repo:`site` (plain HTML and CSS,
   no build step).
``/docs/``
   This documentation, built by Sphinx from :repo:`docs`.

Build and preview
-----------------

.. code-block:: bash

   make site         # docs + site/ into _site/ (the published tree)
   make site-serve   # build, then serve _site on http://127.0.0.1:8001

``make site`` copies :repo:`site` to ``_site/`` and the built docs to
``_site/docs/``; links from the landing page to the documentation are
relative (``docs/...``), so the preview behaves like the published site.

Publishing
----------

The :repo:`.github/workflows/pages.yml` workflow runs ``make site`` and

- on every push to ``develop`` (and when started by hand), deploys
  ``_site`` to GitHub Pages: the site always shows the latest
  ``develop``;
- on pull requests that change ``site/``, ``docs/`` or the workflow,
  only builds it. The strict documentation check (``make docs-check``)
  runs in CI on every pull request.

The repository's Pages source is **GitHub Actions** (*Settings → Pages*);
the ``github-pages`` environment deploys only from ``develop``.

Screenshots and animations
--------------------------

The README's GIFs and screenshots (``docs/_static/readme``) and the
documentation's screenshots (``docs/_static/screenshots``, also used by
the landing page) are recorded from a real Ballet, not drawn:

.. code-block:: bash

   make media

:repo:`scripts/media/record.sh` starts a throwaway Ballet with the
fake model, :repo:`scripts/media/record.mjs` sets up a demo project
over the REST API and drives the web UI with Playwright (Chrome), and
:repo:`scripts/media/gif.py` turns the frames into GIFs with Pillow
(through ``uv``). Re-record after UI changes so the pictures stay true.
