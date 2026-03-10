# Haruki Toolbox Backend

**Haruki Toolbox Backend** is a companion project for [HarukiBot](https://github.com/Team-Haruki), collecting user-submitted suite and mysekai data, and optionally provides public APIs for querying.
It also utilizes Redis for efficient caching to speed up API responses.

## Requirements
+ `PostgreSQL`
+ `MongoDB`
+ `Redis`
+ `Go 1.26.0` (for local development)

## How to Use

1. Go to release page to download `HarukiToolboxBackend`
2. Download `haruki-suite-configs.example.yaml`, and rename it to `haruki-suite-configs.yaml`
3. Make a new directory or use an exists directory
4. Put `HarukiToolboxBackend` and `haruki-suite-configs.yaml` in the same directory
5. Edit `haruki-suite-configs.yaml` and configure it
6. Open Terminal, and `cd` to the directory
7. Run `HarukiToolboxBackend`

## Development Notes

- Start from [`docs/README.md`](./docs/README.md) for the full documentation index.
- See [`docs/development.md`](./docs/development.md) for toolchain/config resolution rules.
- See [`docs/architecture.md`](./docs/architecture.md) for API and module package structure.

## License

This project is licensed under the MIT License.
