package handler

// Batch-3 buffering/decryption paths for payload/benchmark comparisons. The
// MessagePack conversion entry follows the current codec and provider policy.
import (
	"bytes"
	json "encoding/json/v2"
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	harukiAPIData "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/api/data"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/msgpackcodec"
	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/sekai"
	"github.com/klauspost/compress/zstd"
)

func batch3ProcessDataOnce(rawData []byte, server utils.SupportedDataUploadServer, serverCryptor sekai.ServerCryptor) ([]byte, error) {

	msgpackBytes, err := serverCryptor.DecryptToMsgpack(rawData, server)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	encoder.Reset(buf)

	if err := msgpackcodec.WriteJSON(encoder, msgpackBytes, harukiAPIData.ProviderJSONOptions()); err != nil {
		encoder.Close()
		zstdEncoderPool.Put(encoder)
		bytesBufferPool.Put(buf)
		return nil, fmt.Errorf("failed to stream convert msgpack to json+zstd: %w", err)
	}

	msgpackBytes = nil

	if err := encoder.Close(); err != nil {
		zstdEncoderPool.Put(encoder)
		bytesBufferPool.Put(buf)
		return nil, fmt.Errorf("failed to close zstd writer: %w", err)
	}
	zstdEncoderPool.Put(encoder)

	// Copy result before returning buffer to pool
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	bytesBufferPool.Put(buf)
	return result, nil
}

func batch3ProcessDataWithRestore(
	rawData []byte,
	server utils.SupportedDataUploadServer,
	serverCryptor sekai.ServerCryptor,
	suiteRestoreService *SuiteRestoreService,
) ([]byte, error) {
	unpacked, err := serverCryptor.Unpack(rawData, server)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack data: %w", err)
	}
	unpackedMap, ok := unpacked.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unpacked data is not a map")
	}

	restored, _, err := suiteRestoreService.Restore(server, unpackedMap, SuiteRestoreOptions{Purpose: SuiteRestorePurposeSync})
	if err != nil {
		return nil, fmt.Errorf("failed to restore suite data: %w", err)
	}

	jsonBytes, err := json.Marshal(harukiAPIData.NormalizeProviderResponse(restored))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal restored data to json: %w", err)
	}

	buf := bytesBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	encoder := zstdEncoderPool.Get().(*zstd.Encoder)
	encoder.Reset(buf)

	if _, err := encoder.Write(jsonBytes); err != nil {
		encoder.Close()
		zstdEncoderPool.Put(encoder)
		bytesBufferPool.Put(buf)
		return nil, fmt.Errorf("failed to write json to zstd encoder: %w", err)
	}

	jsonBytes = nil

	if err := encoder.Close(); err != nil {
		zstdEncoderPool.Put(encoder)
		bytesBufferPool.Put(buf)
		return nil, fmt.Errorf("failed to close zstd writer: %w", err)
	}
	zstdEncoderPool.Put(encoder)

	// Copy result before returning buffer to pool
	result := make([]byte, buf.Len())
	copy(result, buf.Bytes())
	bytesBufferPool.Put(buf)
	return result, nil
}
