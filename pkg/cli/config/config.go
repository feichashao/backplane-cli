package config

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	logger "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/feichashao/backplane-cli/pkg/info"
	"github.com/feichashao/backplane-cli/pkg/ocm"
)

type JiraTransitionsNamesForAccessRequests struct {
	OnCreation string `json:"on-creation"`
	OnApproval string `json:"on-approval"`
	OnError    string `json:"on-error"`
}

type AccessRequestsJiraConfiguration struct {
	DefaultProject            string                                           `json:"default-project"`
	DefaultIssueType          string                                           `json:"default-issue-type"`
	ProdProject               string                                           `json:"prod-project"`
	ProdIssueType             string                                           `json:"prod-issue-type"`
	ProjectToTransitionsNames map[string]JiraTransitionsNamesForAccessRequests `json:"project-to-transitions-names"`
}

// Please update the validateConfig function if there is any required config key added
type BackplaneConfiguration struct {
	URL                         string                          `json:"url"`
	ProxyURL                    *string                         `json:"proxy-url"`
	SessionDirectory            string                          `json:"session-dir"`
	AssumeInitialArn            string                          `json:"assume-initial-arn"`
	ProdEnvName                 string                          `json:"prod-env-name"`
	PagerDutyAPIKey             string                          `json:"pd-key"`
	JiraBaseURL                 string                          `json:"jira-base-url"`
	JiraToken                   string                          `json:"jira-token"`
	JiraConfigForAccessRequests AccessRequestsJiraConfiguration `json:"jira-config-for-access-requests"`
}

const (
	prodEnvNameKey                 = "prod-env-name"
	jiraBaseURLKey                 = "jira-base-url"
	JiraTokenViperKey              = "jira-token"
	JiraConfigForAccessRequestsKey = "jira-config-for-access-requests"
	prodEnvNameDefaultValue        = "production"
	JiraBaseURLDefaultValue        = "https://issues.redhat.com"
)

var JiraConfigForAccessRequestsDefaultValue = AccessRequestsJiraConfiguration{
	DefaultProject:   "SDAINT",
	DefaultIssueType: "Story",
	ProdProject:      "OHSS",
	ProdIssueType:    "Incident",
	ProjectToTransitionsNames: map[string]JiraTransitionsNamesForAccessRequests{
		"SDAINT": JiraTransitionsNamesForAccessRequests{
			OnCreation: "In Progress",
			OnApproval: "In Progress",
			OnError:    "Closed",
		},
		"OHSS": JiraTransitionsNamesForAccessRequests{
			OnCreation: "Pending Customer",
			OnApproval: "New",
			OnError:    "Cancelled",
		},
	},
}

// GetConfigFilePath returns the Backplane CLI configuration filepath
func GetConfigFilePath() (string, error) {
	// Check if user has explicitly defined backplane config path
	path, found := os.LookupEnv(info.BackplaneConfigPathEnvName)
	if found {
		return path, nil
	}

	UserHomeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	configFilePath := filepath.Join(UserHomeDir, info.BackplaneConfigDefaultFilePath, info.BackplaneConfigDefaultFileName)

	return configFilePath, nil
}

// GetBackplaneConfiguration parses and returns the given backplane configuration
func GetBackplaneConfiguration() (bpConfig BackplaneConfiguration, err error) {
	viper.SetDefault(prodEnvNameKey, prodEnvNameDefaultValue)
	viper.SetDefault(jiraBaseURLKey, JiraBaseURLDefaultValue)
	viper.SetDefault(JiraConfigForAccessRequestsKey, JiraConfigForAccessRequestsDefaultValue)

	filePath, err := GetConfigFilePath()
	if err != nil {
		return bpConfig, err
	}

	viper.AutomaticEnv()

	// Check if the config file exists
	if _, err = os.Stat(filePath); err == nil {
		// Load config file
		viper.SetConfigFile(filePath)
		viper.SetConfigType("json")

		if err := viper.ReadInConfig(); err != nil {
			return bpConfig, err
		}
	}

	if err = validateConfig(); err != nil {
		// FIXME: we should return instead of warning here, after the tests do not require external network access
		logger.Warn(err)
	}

	// Check if user has explicitly defined proxy; it has higher precedence over the config file
	err = viper.BindEnv("proxy-url", info.BackplaneProxyEnvName)
	if err != nil {
		return bpConfig, err
	}

	// Warn user if url defined in the config file
	if viper.GetString("url") != "" {
		logger.Warn("Manual URL configuration is deprecated, please remove URL key from Backplane configuration")
	}

	// Warn if user has explicitly defined backplane URL via env
	url, ok := getBackplaneEnv(info.BackplaneURLEnvName)
	if ok {
		logger.Warn(fmt.Printf("Manual URL configuration is deprecated, please unset the environment %s", info.BackplaneURLEnvName))
		bpConfig.URL = url
	} else {
		// Fetch backplane URL from ocm env
		if bpConfig.URL, err = bpConfig.GetBackplaneURL(); err != nil {
			return bpConfig, err
		}
	}

	// proxyURL is required
	proxyInConfigFile := viper.GetStringSlice("proxy-url")
	proxyURL := bpConfig.getFirstWorkingProxyURL(proxyInConfigFile)
	if proxyURL != "" {
		bpConfig.ProxyURL = &proxyURL
	}

	bpConfig.SessionDirectory = viper.GetString("session-dir")
	bpConfig.AssumeInitialArn = viper.GetString("assume-initial-arn")

	// pagerDuty token is optional
	pagerDutyAPIKey := viper.GetString("pd-key")
	if pagerDutyAPIKey != "" {
		bpConfig.PagerDutyAPIKey = pagerDutyAPIKey
	} else {
		logger.Info("No PagerDuty API Key configuration available. This will result in failure of `ocm-backplane login --pd <incident-id>` command.")
	}

	// OCM prod env name is optional as there is a default value
	bpConfig.ProdEnvName = viper.GetString(prodEnvNameKey)

	// JIRA base URL is optional as there is a default value
	bpConfig.JiraBaseURL = viper.GetString(jiraBaseURLKey)

	// JIRA token is optional
	bpConfig.JiraToken = viper.GetString(JiraTokenViperKey)

	// JIRA config for access requests is optional as there is a default value
	err = viper.UnmarshalKey(JiraConfigForAccessRequestsKey, &bpConfig.JiraConfigForAccessRequests)

	if err != nil {
		logger.Warnf("failed to unmarshal '%s' entry as json in '%s' config file: %v", JiraConfigForAccessRequestsKey, filePath, err)
	} else {
		for _, project := range []string{bpConfig.JiraConfigForAccessRequests.DefaultProject, bpConfig.JiraConfigForAccessRequests.ProdProject} {
			if _, isKnownProject := bpConfig.JiraConfigForAccessRequests.ProjectToTransitionsNames[project]; !isKnownProject {
				logger.Warnf("content unmarshalled from '%s' in '%s' config file is inconsistent: no transitions defined for project '%s'", JiraConfigForAccessRequestsKey, filePath, project)
			}
		}
	}

	return bpConfig, nil
}

var clientDo = func(client *http.Client, req *http.Request) (*http.Response, error) {
	return client.Do(req)
}

func (config *BackplaneConfiguration) getFirstWorkingProxyURL(s []string) string {
	bpURL := config.URL + "/healthz"

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, p := range s {
		proxyURL, err := url.ParseRequestURI(p)
		if err != nil {
			logger.Debugf("proxy-url: '%v' could not be parsed.", p)
			continue
		}

		client.Transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		req, _ := http.NewRequest("GET", bpURL, nil)
		resp, err := clientDo(client, req)
		if err != nil {
			logger.Infof("Proxy: %s returned an error: %s", proxyURL, err)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return p
		}
		logger.Infof("proxy: %s did not pass healthcheck, expected response code 200, got %d, discarding", p, resp.StatusCode)
	}

	if len(s) > 0 {
		logger.Infof("falling back to first proxy-url after all proxies failed health checks: %s", s[0])
		return s[0]
	}

	return ""
}

func validateConfig() error {

	// Validate the proxy url
	if viper.GetStringSlice("proxy-url") == nil && os.Getenv(info.BackplaneProxyEnvName) == "" {
		return fmt.Errorf("proxy-url must be set explicitly in either config file or via the environment HTTPS_PROXY")
	}

	return nil
}

// GetConfigDirectory returns the backplane config path
func GetConfigDirectory() (string, error) {
	bpConfigFilePath, err := GetConfigFilePath()
	if err != nil {
		return "", err
	}
	configDirectory := filepath.Dir(bpConfigFilePath)

	return configDirectory, nil
}

// GetBackplaneURL returns API URL
func (config *BackplaneConfiguration) GetBackplaneURL() (string, error) {

	ocmEnv, err := ocm.DefaultOCMInterface.GetOCMEnvironment()
	if err != nil {
		return "", err
	}
	url, ok := ocmEnv.GetBackplaneURL()
	if !ok {
		return "", fmt.Errorf("the requested API endpoint is not available for the OCM environment: %v", ocmEnv.Name())
	}
	logger.Infof("Backplane URL retrieved via OCM environment: %s", url)
	return url, nil
}

// getBackplaneEnv retrieves the value of the environment variable named by the key
func getBackplaneEnv(key string) (string, bool) {
	val, ok := os.LookupEnv(key)
	if ok {
		logger.Infof("Backplane key %s set via env vars: %s", key, val)
		return val, ok
	}
	return "", false
}

// CheckAPIConnection validate API connection via configured proxy and VPN
func (config BackplaneConfiguration) CheckAPIConnection() error {

	// make test api connection
	connectionOk, err := config.testHTTPRequestToBackplaneAPI()

	if !connectionOk {
		return err
	}

	return nil
}

// testHTTPRequestToBackplaneAPI returns status of the API connection
func (config BackplaneConfiguration) testHTTPRequestToBackplaneAPI() (bool, error) {
	client := http.Client{
		Timeout: 5 * time.Second,
	}

	if config.ProxyURL != nil {
		proxyURL, err := url.Parse(*config.ProxyURL)
		if err != nil {
			return false, err
		}
		http.DefaultTransport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	}

	req, err := http.NewRequest("HEAD", config.URL, nil)
	if err != nil {
		return false, err
	}
	_, err = client.Do(req)
	if err != nil {
		return false, err
	}

	return true, nil
}
