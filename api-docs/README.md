# Kai API Docs

Static API documentation project for the Kai platform. The docs are powered by Redoc and the source of truth is the OpenAPI spec in `openapi/openapi.yaml`.

## Getting started

1. Install dependencies (Node.js 18+ recommended):
   ```bash
   npm install
   ```
2. Start a local preview server with live reload:
   ```bash
   npm run dev
   ```
3. Build a static bundle to `dist/index.html`:
   ```bash
   npm run build
   ```

## Editing the spec

- Update `openapi/openapi.yaml` with new endpoints, schemas, and examples.
- Set the default server URL in the `servers` block to match your environment.
- The Redoc bundle automatically reads from the spec file; no additional wiring is needed.

## Notes

- The project is self-contained and does not affect the rest of the repository.
- If you prefer another generator (e.g., Stoplight, Slate, or Docusaurus), replace `redoc-cli` in `package.json` while keeping the spec file layout.
