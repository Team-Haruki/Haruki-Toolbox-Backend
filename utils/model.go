package utils

import "time"

type SekaiDataRetrieverResponse struct {
	RawBody    []byte            `json:"raw_body"`
	StatusCode int               `json:"status_code"`
	NewHeaders map[string]string `json:"new_headers,omitempty"`
}

type SekaiInheritDataRetrieverResponse struct {
	Server  string `json:"server"`
	UserID  int64  `json:"user_id"`
	Suite   []byte `json:"suite,omitempty"`
	Mysekai []byte `json:"mysekai,omitempty"`
}

type InheritInformation struct {
	InheritID       string `json:"inherit_id"`
	InheritPassword string `json:"inherit_password"`
}

type DataChunk struct {
	RequestURL  string    `json:"request_url"`
	UploadID    string    `json:"upload_id"`
	ChunkIndex  int       `json:"chunk_index"`
	TotalChunks int       `json:"total_chunks"`
	Time        time.Time `json:"time"`
	Data        []byte    `json:"data"`
}

type HandleDataResult struct {
	Status       *int    `json:"status,omitempty"`
	ErrorMessage *string `json:"error_message,omitempty"`
	UserID       *int64  `json:"user_id,omitempty"`
}
