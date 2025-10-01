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

func NewHarukiSekaiAPIClient(apiEndpoint, apiToken string) *HarukiSekaiAPIClient {
	return &HarukiSekaiAPIClient{
		httpClient:             resty.New(),
		harukiSekaiAPIEndpoint: apiEndpoint,
		harukiSekaiAPIToken:    apiToken,
	}
}

func (c *HarukiSekaiAPIClient) GetUserProfile(userID string, serverStr string) (bool, []byte, error) {
	server, err := utils.ParseSupportedDataUploadServer(serverStr)
	if err != nil {
		return false, nil, err
	}
	var url string
	if server == utils.SupportedDataUploadServerEN || server == utils.SupportedDataUploadServerKR {
		url = fmt.Sprintf("%s/api/%s/user/%s/profile", c.harukiSekaiAPIEndpoint, serverStr, userID)
	} else {
		url = fmt.Sprintf("%s/api/%s/user/%%25user_id/%s/profile", c.harukiSekaiAPIEndpoint, serverStr, userID)
	}
	resp, err := c.httpClient.R().
		SetHeader("X-Haruki-Sekai-Token", c.harukiSekaiAPIToken).
		SetHeader("Accept", "application/json").
		Get(url)
	if err != nil {
		return false, nil, fmt.Errorf("请求失败: %v", err)
	}
	switch resp.StatusCode() {
	case 200:
		return true, resp.Body(), nil
	case 404:
		fmt.Println("Get User Profile Not Found")
		return true, nil, fmt.Errorf("this user does not exist, please check your userID if is corrent")
	case 500:
		return false, nil, fmt.Errorf("api is busy, please try again later, if this problem still consist, please contact Haruki Dev Team")
	case 503:
		return false, nil, fmt.Errorf("the game server you query is under maintenance")
	default:
		return false, nil, fmt.Errorf("api request failed, status code: %d", resp.StatusCode())
	}

}
