# Contributing

Thanks for contributing to NetFerry. Please read this guide before opening a PR.

## Contribution Workflow

1. Fork the repository and create a feature branch.
2. Keep commits focused and messages clear.
3. Run local build checks before submitting.
4. Open a PR and complete the template sections.

## Local Checks

### Desktop app

```bash
cd netferry-desktop
npm install
npm run build
cd src-tauri && cargo check
```

### Python wrapper

```bash
python3 -m py_compile netferry/__init__.py netferry/__main__.py
```

## Coding Guidelines

- Avoid unrelated formatting-only noise in commits.
- Never commit sensitive data (keys, tokens, certificates, real credentials).
- For behavior changes, include reproducible verification steps.

