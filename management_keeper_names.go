package main

import (
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func managementKeeperAuthNames(force bool) pluginapi.ManagementResponse {
	response := managementJSON(http.StatusOK, keeperAuthNamesForConfig(loadedConfig(), force))
	response.Headers.Set("Cache-Control", "no-store")
	return response
}
