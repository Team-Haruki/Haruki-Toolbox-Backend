import msgpack
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

_aes_key = b''
_aes_iv = b''

async def padding(s):
    return s + (16 - len(s) % 16) * bytes([16 - len(s) % 16])


async def pack(content):
    cipher = Cipher(algorithms.AES(_aes_key), modes.CBC(_aes_iv), backend=default_backend())
    encryptor = cipher.encryptor()
    ss = msgpack.packb(content, use_single_float=True)

    ss = await padding(ss)
    encrypted = encryptor.update(ss) + encryptor.finalize()
    return encrypted


async def unpack(content):
    cipher = Cipher(algorithms.AES(_aes_key), modes.CBC(_aes_iv), backend=default_backend())
    decryptor = cipher.decryptor()
    decrypted = decryptor.update(content) + decryptor.finalize()
    return msgpack.unpackb(decrypted[:-decrypted[-1]], strict_map_key=False)
