# Vendored: blobmascot core

The files under `core/`, `geometry/`, `face/`, `motion/`, `render/`, `fx/`
and `export/` are copied unchanged from **blobmascot v0.2.0**
(https://github.com/Naandalist/blobmascot), MIT licensed — see `LICENSE`
in this folder. Copyright (c) 2026 Listiananda Apriliawan.

Only the framework-free core is vendored. The package's `src/react/` and
its root entry import React, so the npm package is not installed; the
Svelte side lives one level up (`../BlobAvatar.svelte`, `../blob.ts`).

To update: copy the same folders from a newer release over these, keep
`LICENSE` current, and re-run `npm run test:unit` in `fe/common/avatar`.
Do not edit the vendored files in place — wrap them in `../blob.ts`.
