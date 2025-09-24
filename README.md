# Haruki Toolbox Backend

**Haruki Toolbox Backend API** is a companion project for [HarukiBot](https://github.com/Team-Haruki), collecting user-submitted suite and mysekai data, and optionally provides public APIs for querying.
It also utilizes Redis for efficient caching to speed up API responses.

## Requirements
+ `MongoDB`
+ `Redis`

## How to Use

1. Go to release page to download `HarukiToolboxBackend`
2. Download `haruki-suite-configs.example.yaml`, and rename it to `haruki-suite-configs.yaml`
3. Make a new directory or use an exists directory
4. Put `HarukiToolboxBackend` and `haruki-suite-configs.yaml` in the same directory
5. Edit `haruki-suite-configs.yaml` and configure it
6. Open Terminal, and `cd` to the directory
7. Run `HarukiToolboxBackend`

## License

This project is licensed under the MIT License.