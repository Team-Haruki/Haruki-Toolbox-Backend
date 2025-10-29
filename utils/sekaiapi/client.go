package sekaiapi

import (
	"fmt"
	"haruki-suite/utils"

	"github.com/go-resty/resty/v2"
)

type HarukiSekaiAPIClient struct {
	httpClient             *resty.Client
	harukiSekaiAPIEndpoint string
	harukiSekaiAPIToken    string
}

type HarukiSekaiAPIResult struct {
	ServerAvailable bool
	AccountExists   bool
	Body            bool
}

func NewHarukiSekaiAPIClient(apiEndpoint, apiToken string) *HarukiSekaiAPIClient {
	return &HarukiSekaiAPIClient{
		httpClient:             resty.New(),
		harukiSekaiAPIEndpoint: apiEndpoint,
		harukiSekaiAPIToken:    apiToken,
	}
}

func (c *HarukiSekaiAPIClient) GetUserProfile(userID string, serverStr string) (*HarukiSekaiAPIResult, []byte, error) {
	_, err := utils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return nil, nil, err
	}
	url := fmt.Sprintf("%s/api/%s/%s/profile", c.harukiSekaiAPIEndpoint, serverStr, userID)
	resp, err := c.httpClient.R().
		SetHeader("X-Haruki-Sekai-Token", c.harukiSekaiAPIToken).
		SetHeader("Accept", "application/json").
		Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("请求失败: %v", err)
	}
	switch resp.StatusCode() {
	case 200:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   true,
			Body:            true,
		}, resp.Body(), nil
	case 404:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("this user does not exist, please check your userID if is corrent")
	case 500:
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("api is busy, please try again later, if this problem still consist, please contact Haruki Dev Team")
	case 503:
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("the game server you query is under maintenance")
	default:
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("api request failed, status code: %d", resp.StatusCode())
	}
}
