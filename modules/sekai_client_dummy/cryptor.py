import msgpack
from typing import Union, Dict, List, Any
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes

from modules.enums import SupportedSuiteUploadServer

general_aes_key = bytes.fromhex("")
general_aes_iv = bytes.fromhex("")
en_aes_key = bytes.fromhex("")
en_aes_iv = bytes.fromhex("")


def _get_cipher(server: SupportedSuiteUploadServer) -> Cipher:
    if server == SupportedSuiteUploadServer.en:
        key, iv = en_aes_key, en_aes_iv
    else:
        key, iv = general_aes_key, general_aes_iv
    return Cipher(algorithms.AES(key), modes.CBC(iv), backend=default_backend())


def _pad(data: bytes) -> bytes:
    padding_len = 16 - len(data) % 16
    return data + bytes([padding_len]) * padding_len


def pack(content, server: SupportedSuiteUploadServer = SupportedSuiteUploadServer.jp) -> bytes:
    cipher = _get_cipher(server)
    encryptor = cipher.encryptor()
    packed = msgpack.packb(content, use_single_float=True)
    padded = _pad(packed)
    return encryptor.update(padded) + encryptor.finalize()


def unpack(
    content, server: SupportedSuiteUploadServer = SupportedSuiteUploadServer.jp
) -> Union[Dict[str, Any], List[Any]]:
    cipher = _get_cipher(server)
    decryptor = cipher.decryptor()
    decrypted = decryptor.update(content) + decryptor.finalize()
    return msgpack.unpackb(decrypted[: -decrypted[-1]], strict_map_key=False)
