# NetFerry Desktop

NetFerry Desktop is the desktop client for NetFerry, built with Tauri v2 + React + TypeScript.

## Development

```bash
npm install
npm run tauri dev
```

## Build

```bash
npm run build
npm run build:sidecar
npm run tauri build
```

## Directory Overview

- `src/`: Frontend UI
- `src-tauri/`: Rust backend and packaging config
- `../scripts/build_sidecar.py`: Builds the `netferry-tunnel` sidecar
