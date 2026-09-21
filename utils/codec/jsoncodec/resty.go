package jsoncodec

import "github.com/go-resty/resty/v2"

// ConfigureResty selects the same JSON v2 policy for implicit request bodies
// and SetResult decoding as for explicit application JSON calls.
func ConfigureResty(client *resty.Client) *resty.Client {
	client.JSONMarshal = Marshal
	client.JSONUnmarshal = Unmarshal
	return client
}
