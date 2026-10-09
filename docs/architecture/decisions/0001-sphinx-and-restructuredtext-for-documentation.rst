ADR-0001: Sphinx and reStructuredText for Project Documentation
===============================================================

:Status: Accepted
:Date: 2026-10-01

.. note::

   Since :doc:`0027-tenancy-platform-organization-project`, customers are
   called **organizations** and the installation-wide level formerly
   called the organization is the **platform**; this record keeps the
   terms of its time.

Context
-------

Ballet is documentation-driven: documentation is written alongside code
by humans and agents, and must stay verifiable. Cross-references between
concepts, architecture and ADRs must not silently break. The documentation
must be publishable as a static site.

Decision
--------

Project documentation is a Sphinx site written in reStructuredText under
``docs/``. ``docs/`` is a self-contained ``uv`` Python project. ``make
docs-check`` runs a strict build (warnings are errors) and a link check,
and must pass in CI.

This covers documentation *about Ballet*. Knowledge Ballet manages for
customers lives in the Knowledge service.

Alternatives Considered
-----------------------

Markdown with MkDocs
~~~~~~~~~~~~~~~~~~~~

Advantages:

- Familiar syntax; very low barrier.

Disadvantages:

- Weaker cross-reference checking; broken links are easier to miss.

Markdown files without a site generator
~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

Advantages:

- Zero tooling.

Disadvantages:

- No build-time verification, no navigation, no diagrams.

Decision Criteria
-----------------

Link integrity, verifiability in CI, diagram support, maintainability.

Rationale
---------

Sphinx's roles (``:doc:``, ``:ref:``) are resolved at build time, so a
strict build catches broken references — important when agents write
much of the documentation.

Consequences
------------

Positive
~~~~~~~~

- Broken references fail CI.

Negative
~~~~~~~~

- reStructuredText is less familiar than Markdown.

Risks
~~~~~

- None significant.

Follow-up
~~~~~~~~~

- Publish the site (e.g. GitHub Pages) once the repository is hosted.

References
----------

- :doc:`/architecture/overview`
