# Haruki Suite DB API

**Haruki Suite DB API** is a companion project for [HarukiBot](https://github.com/Team-Haruki), collecting user-submitted suite and mysekai data, and optionally provides public APIs for querying.
It also utilizes `fastapi-cache2` for efficient caching to speed up API responses.

## Requirements
+ `MongoDB`
+ `Redis`

## How to Use

1. Rename `configs.example.py` to `configs.py` and then configure it.
2. Install [uv](https://github.com/astral-sh/uv) to manage and install project dependencies.
3. Run the following command to install dependencies:
   ```bash
   uv sync
   ```
4. (Optional) If you're on **Linux/macOS**, it's recommended to install [uvloop](https://github.com/MagicStack/uvloop) for better performance:
   ```bash
   uv add uvloop
   ```
5. If you need to change the listening address or other server settings, edit the `hypercorn.toml` file. If you have installed uvloop, uncomment the `worker_class` line in `hypercorn.toml` to enable it. 
6. Finally, run the server using:
   ```bash
   uv run hypercorn app:app --config hypercorn.toml
   ```

## License

This project is licensed under the MIT License.