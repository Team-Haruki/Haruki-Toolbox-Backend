# Haruki Toolbox Backend

**Haruki Toolbox Backend** is a companion project for [HarukiBot](https://github.com/Team-Haruki), collecting user-submitted suite and mysekai data, and optionally provides public APIs for querying.
It also utilizes Redis for efficient caching to speed up API responses.

## Requirements
+ `PostgreSQL`
+ `PostgreSQL` 游戏数据存储
+ `Redis`
+ `Go 1.27.1` (for local development)

## How to Use

1. Go to release page to download `HarukiToolboxBackend-linux-amd64.tar.gz` or `HarukiToolboxBackend-linux-arm64.tar.gz` (the container image is `ghcr.io/team-haruki/haruki-toolbox-backend`)
2. Extract it into a new directory or an existing one; the archive contains `HarukiToolboxBackend`, the `data/` directory and `haruki-toolbox-configs.example.yaml`
3. Copy `haruki-toolbox-configs.example.yaml` to `haruki-toolbox-configs.yaml` in the same directory, keeping `data/` next to it
4. Edit `haruki-toolbox-configs.yaml` and configure it (`${ENV_VAR}` placeholders are expanded; set `HARUKI_CONFIG_PATH` to load a config file from elsewhere)
5. Open Terminal, and `cd` to the directory
6. Run `HarukiToolboxBackend`

## Development Notes

- Start from [`docs/README.md`](./docs/README.md) for the full documentation index.
- See [`docs/backend-architecture.zh-CN.md`](./docs/backend-architecture.zh-CN.md) for module boundaries and allowed dependency directions.
- Third-party OAuth2 / OIDC integration (authorization code with PKCE, confidential clients, and the device authorization grant for headless programs, RFC 8628) is in [`docs/oauth2-integration.zh-CN.md`](./docs/oauth2-integration.zh-CN.md); how the backend runs Kratos, Hydra and Oathkeeper, including device-flow operations, is in [`docs/ory-suite-usage.zh-CN.md`](./docs/ory-suite-usage.zh-CN.md).
- See [`AGENTS.md`](./AGENTS.md) for build, test, code generation and commit conventions.

## License

This project is licensed under the MIT License.
