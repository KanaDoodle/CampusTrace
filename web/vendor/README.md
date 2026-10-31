Browser-local resume parsing dependencies (bundled; no runtime CDN):

- `pdf.min.mjs`, `pdf.worker.min.mjs`: `pdfjs-dist` 6.4.299, Apache-2.0. Source: https://github.com/mozilla/pdf.js. License: `PDFJS-LICENSE.txt`.
- `jszip.min.js`: `jszip` 3.10.2, MIT or GPLv3. Source: https://github.com/Stuk/jszip. License: `JSZIP-LICENSE.md` (MIT option used).

These files are copied from their published npm tarballs without modification.

Tarball integrity and bundled file digests are recorded in `provenance.json`. PDF reader and worker are upgraded together.
