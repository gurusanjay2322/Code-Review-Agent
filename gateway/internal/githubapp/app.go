package githubapp

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

// App mints installation-scoped clients. Installation tokens are cached and refreshed
// by ghinstallation, so one transport per installation is reused across jobs.
type App struct {
	apiURL string
	apps   *ghinstallation.AppsTransport

	mu    sync.Mutex
	cache map[int64]*Client
}

func NewApp(appID int64, privateKeyPath, apiURL string) (*App, error) {
	atr, err := ghinstallation.NewAppsTransportKeyFromFile(http.DefaultTransport, appID, privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("load GitHub App key: %w", err)
	}
	atr.BaseURL = apiURL
	return &App{apiURL: apiURL, apps: atr, cache: map[int64]*Client{}}, nil
}

func (a *App) Installation(installationID int64) *Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.cache[installationID]; ok {
		return c
	}
	itr := ghinstallation.NewFromAppsTransport(a.apps, installationID)
	itr.BaseURL = a.apiURL
	c := NewClient(a.apiURL, &http.Client{Transport: itr, Timeout: 60 * time.Second})
	a.cache[installationID] = c
	return c
}
