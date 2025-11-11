// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/openchoreo/openchoreo/internal/openchoreo-api/middleware/logger"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/models"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/services"
)

// GetConfigurations retrieves all configurations for a component in a specific environment
func (h *Handler) GetConfigurations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logger.GetLogger(ctx)
	logger.Debug("GetConfigurations handler called")

	// Extract path parameters
	orgName := r.PathValue("orgName")
	projectName := r.PathValue("projectName")
	componentName := r.PathValue("componentName")
	envName := r.PathValue("envName")

	if orgName == "" || projectName == "" || componentName == "" || envName == "" {
		logger.Warn("All path parameters are required")
		writeErrorResponse(w, http.StatusBadRequest, "Organization, project, component, and environment names are required", "INVALID_PARAMS")
		return
	}

	// Call service to get configurations
	configurations, err := h.services.ConfigurationService.GetConfigurations(ctx, orgName, projectName, componentName, envName)
	if err != nil {
		logger.Error("Failed to get configurations", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "Internal server error", services.CodeInternalError)
		return
	}

	// Success response
	logger.Debug("Retrieved configurations successfully",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)
	writeSuccessResponse(w, http.StatusOK, configurations)
}

// UpsertConfigurations creates or updates configurations for a component in a specific environment
func (h *Handler) UpsertConfigurations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logger.GetLogger(ctx)
	logger.Debug("UpsertConfigurations handler called")

	// Extract path parameters
	orgName := r.PathValue("orgName")
	projectName := r.PathValue("projectName")
	componentName := r.PathValue("componentName")
	envName := r.PathValue("envName")

	if orgName == "" || projectName == "" || componentName == "" || envName == "" {
		logger.Warn("All path parameters are required")
		writeErrorResponse(w, http.StatusBadRequest, "Organization, project, component, and environment names are required", "INVALID_PARAMS")
		return
	}

	// Parse request body
	var req models.UpsertConfigurationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("Invalid JSON body", "error", err)
		writeErrorResponse(w, http.StatusBadRequest, "Invalid request body", "INVALID_JSON")
		return
	}
	defer r.Body.Close()

	// Validate that at least one configuration is provided
	if len(req.Env) == 0 && len(req.Files) == 0 {
		logger.Warn("No configurations provided")
		writeErrorResponse(w, http.StatusBadRequest, "At least one environment variable or file configuration must be provided", "INVALID_REQUEST")
		return
	}

	// Call service to upsert configurations
	configurations, err := h.services.ConfigurationService.UpsertConfigurations(ctx, orgName, projectName, componentName, envName, &req)
	if err != nil {
		if errors.Is(err, services.ErrComponentDeploymentNotFound) {
			logger.Warn("ComponentDeployment not found", "org", orgName, "project", projectName, "component", componentName, "environment", envName)
			writeErrorResponse(w, http.StatusNotFound, "ComponentDeployment not found. Please promote the component to this environment first.", services.CodeComponentDeploymentNotFound)
			return
		}
		logger.Error("Failed to upsert configurations", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "Internal server error", services.CodeInternalError)
		return
	}

	// Success response
	logger.Debug("Upserted configurations successfully",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)
	writeSuccessResponse(w, http.StatusOK, configurations)
}

// DeleteConfigurations deletes configurations for a component in a specific environment
func (h *Handler) DeleteConfigurations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logger.GetLogger(ctx)
	logger.Debug("DeleteConfigurations handler called")

	// Extract path parameters
	orgName := r.PathValue("orgName")
	projectName := r.PathValue("projectName")
	componentName := r.PathValue("componentName")
	envName := r.PathValue("envName")

	if orgName == "" || projectName == "" || componentName == "" || envName == "" {
		logger.Warn("All path parameters are required")
		writeErrorResponse(w, http.StatusBadRequest, "Organization, project, component, and environment names are required", "INVALID_PARAMS")
		return
	}

	// Extract keys from query parameter (comma-separated)
	keysParam := r.URL.Query().Get("keys")
	var keys []string
	if keysParam != "" {
		keys = strings.Split(keysParam, ",")
		// Trim whitespace from each key
		for i := range keys {
			keys[i] = strings.TrimSpace(keys[i])
		}
	}

	// Call service to delete configurations
	err := h.services.ConfigurationService.DeleteConfigurations(ctx, orgName, projectName, componentName, envName, keys)
	if err != nil {
		logger.Error("Failed to delete configurations", "error", err)
		writeErrorResponse(w, http.StatusInternalServerError, "Internal server error", services.CodeInternalError)
		return
	}

	// Success response
	if len(keys) == 0 {
		logger.Debug("Deleted all configurations successfully",
			"org", orgName,
			"project", projectName,
			"component", componentName,
			"environment", envName,
		)
	} else {
		logger.Debug("Deleted specific configurations successfully",
			"org", orgName,
			"project", projectName,
			"component", componentName,
			"environment", envName,
			"keys", keys,
		)
	}

	// Return 204 No Content for successful deletion
	w.WriteHeader(http.StatusNoContent)
}
