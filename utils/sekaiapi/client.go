package sekaiapi

import (
	"encoding/json"
	"fmt"
	"haruki-suite/utils"
	harukiLogger "haruki-suite/utils/logger"

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
		harukiLogger.Errorf("Sekai API request failed for %s: %v", url, err)
		return nil, nil, fmt.Errorf("请求失败: %v", err)
	}

	statusCode := resp.StatusCode()
	body := resp.Body()

	// For all responses, try to detect body-level errors (upstream may return 200 with error payload)
	if len(body) > 0 {
		var bodyData map[string]any
		if jsonErr := json.Unmarshal(body, &bodyData); jsonErr == nil {
			if _, hasError := bodyData["errorCode"]; hasError {
				// Body contains an error payload — extract the real status
				errMsg, _ := bodyData["errorMessage"].(string)
				httpStatus := 0
				if hs, ok := bodyData["httpStatus"].(float64); ok {
					httpStatus = int(hs)
				}
				switch {
				case httpStatus == 404 || statusCode == 404:
					return &HarukiSekaiAPIResult{
						ServerAvailable: true,
						AccountExists:   false,
						Body:            false,
					}, nil, fmt.Errorf("this user does not exist: %s", errMsg)
				case httpStatus == 503 || statusCode == 503:
					return &HarukiSekaiAPIResult{
						ServerAvailable: false,
						AccountExists:   false,
						Body:            false,
					}, nil, fmt.Errorf("game server under maintenance: %s", errMsg)
				case httpStatus >= 500 || statusCode >= 500:
					harukiLogger.Errorf("Sekai API returned server error for %s: %s", url, errMsg)
					return &HarukiSekaiAPIResult{
						ServerAvailable: false,
						AccountExists:   false,
						Body:            false,
					}, nil, fmt.Errorf("api server error: %s", errMsg)
				default:
					return &HarukiSekaiAPIResult{
						ServerAvailable: true,
						AccountExists:   false,
						Body:            false,
					}, nil, fmt.Errorf("api error (status %d): %s", httpStatus, errMsg)
				}
			}
		}
	}

	switch statusCode {
	case 200:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   true,
			Body:            true,
		}, body, nil
	case 404:
		return &HarukiSekaiAPIResult{
			ServerAvailable: true,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("this user does not exist, please check your userID if is corrent")
	case 500:
		harukiLogger.Errorf("Sekai API returned 500 for %s", url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("api is busy, please try again later, if this problem still consist, please contact Haruki Dev Team")
	case 503:
		harukiLogger.Warnf("Sekai API returned 503 (maintenance) for %s", url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("the game server you query is under maintenance")
	default:
		harukiLogger.Errorf("Sekai API returned unexpected status %d for %s", statusCode, url)
		return &HarukiSekaiAPIResult{
			ServerAvailable: false,
			AccountExists:   false,
			Body:            false,
		}, nil, fmt.Errorf("api request failed, status code: %d", statusCode)
	}
}
