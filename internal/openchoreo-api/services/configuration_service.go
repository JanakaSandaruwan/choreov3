// Copyright 2025 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package services

import (
	"context"
	"encoding/base64"
	"fmt"

	"golang.org/x/exp/slog"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	choreoapis "github.com/openchoreo/openchoreo/api/v1alpha1"
	"github.com/openchoreo/openchoreo/internal/openchoreo-api/models"
)

// ConfigurationService handles configuration operations
type ConfigurationService struct {
	k8sClient client.Client
	logger    *slog.Logger
}

// NewConfigurationService creates a new ConfigurationService instance
func NewConfigurationService(k8sClient client.Client, logger *slog.Logger) *ConfigurationService {
	return &ConfigurationService{
		k8sClient: k8sClient,
		logger:    logger,
	}
}

// GetConfigurations retrieves all configurations for a component in a specific environment
func (s *ConfigurationService) GetConfigurations(ctx context.Context, orgName, projectName, componentName, envName string) (*models.ConfigurationResponse, error) {
	s.logger.Debug("Getting configurations",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)

	// Get the ComponentDeployment resource
	componentDeploymentName := fmt.Sprintf("%s-%s", componentName, envName)
	componentDeployment := &choreoapis.ComponentDeployment{}
	err := s.k8sClient.Get(ctx, types.NamespacedName{
		Name:      componentDeploymentName,
		Namespace: orgName,
	}, componentDeployment)

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ComponentDeployment doesn't exist - return empty configurations
			s.logger.Debug("ComponentDeployment not found, returning empty configurations",
				"org", orgName,
				"project", projectName,
				"component", componentName,
				"environment", envName,
			)
			return &models.ConfigurationResponse{
				Env:   []models.EnvVarResponse{},
				Files: []models.FileConfigurationResponse{},
			}, nil
		}
		s.logger.Error("Failed to get ComponentDeployment", "error", err)
		return nil, fmt.Errorf("failed to get ComponentDeployment: %w", err)
	}

	// Extract configurations from ComponentDeployment
	response := &models.ConfigurationResponse{
		Env:   []models.EnvVarResponse{},
		Files: []models.FileConfigurationResponse{},
	}

	if componentDeployment.Spec.ConfigurationOverrides != nil {
		// Process environment variables
		for _, envVar := range componentDeployment.Spec.ConfigurationOverrides.Env {
			envResponse := models.EnvVarResponse{
				Key: envVar.Key,
			}

			// Check if it's a secret reference or direct value
			if envVar.ValueFrom != nil && envVar.ValueFrom.SecretRef != nil {
				envResponse.SecretRef = &models.SecretRefDetails{
					Name: envVar.ValueFrom.SecretRef.Name,
					Key:  envVar.ValueFrom.SecretRef.Key,
				}
			} else if envVar.Value != "" {
				envResponse.Value = envVar.Value
			}

			response.Env = append(response.Env, envResponse)
		}

		// Process file configurations
		for _, fileVar := range componentDeployment.Spec.ConfigurationOverrides.Files {
			fileResponse := models.FileConfigurationResponse{
				Key:       fileVar.Key,
				MountPath: fileVar.MountPath,
			}

			// Check if it's a secret reference or direct value
			if fileVar.ValueFrom != nil && fileVar.ValueFrom.SecretRef != nil {
				fileResponse.SecretRef = &models.SecretRefDetails{
					Name: fileVar.ValueFrom.SecretRef.Name,
					Key:  fileVar.ValueFrom.SecretRef.Key,
				}
			} else if fileVar.Value != "" {
				// Base64 encode the file content
				encodedContent := base64.StdEncoding.EncodeToString([]byte(fileVar.Value))
				fileResponse.Value = encodedContent
			}

			response.Files = append(response.Files, fileResponse)
		}
	}

	s.logger.Debug("Retrieved configurations successfully",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
		"envCount", len(response.Env),
		"filesCount", len(response.Files),
	)

	return response, nil
}

// UpsertConfigurations creates or updates configurations for a component in a specific environment
func (s *ConfigurationService) UpsertConfigurations(ctx context.Context, orgName, projectName, componentName, envName string, req *models.UpsertConfigurationRequest) (*models.ConfigurationResponse, error) {
	s.logger.Debug("Upserting configurations",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)

	// Get or create ComponentDeployment
	componentDeploymentName := fmt.Sprintf("%s-%s", componentName, envName)
	componentDeployment := &choreoapis.ComponentDeployment{}
	err := s.k8sClient.Get(ctx, types.NamespacedName{
		Name:      componentDeploymentName,
		Namespace: orgName,
	}, componentDeployment)

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ComponentDeployment doesn't exist - cannot create configurations without it
			s.logger.Warn("ComponentDeployment not found",
				"org", orgName,
				"project", projectName,
				"component", componentName,
				"environment", envName,
			)
			return nil, ErrComponentDeploymentNotFound
		}
		s.logger.Error("Failed to get ComponentDeployment", "error", err)
		return nil, fmt.Errorf("failed to get ComponentDeployment: %w", err)
	}

	// Initialize ConfigurationOverrides if nil
	if componentDeployment.Spec.ConfigurationOverrides == nil {
		componentDeployment.Spec.ConfigurationOverrides = &choreoapis.EnvConfigurationOverrides{
			Env:   []choreoapis.EnvVar{},
			Files: []choreoapis.FileVar{},
		}
	}

	// Merge environment variables
	envMap := make(map[string]choreoapis.EnvVar)
	for _, envVar := range componentDeployment.Spec.ConfigurationOverrides.Env {
		envMap[envVar.Key] = envVar
	}

	for _, envReq := range req.Env {
		newEnvVar := choreoapis.EnvVar{
			Key: envReq.Key,
		}

		// Check if it's a secret reference or direct value
		if envReq.SecretRef != nil {
			newEnvVar.ValueFrom = &choreoapis.EnvVarValueFrom{
				SecretRef: &choreoapis.SecretKeyRef{
					Name: envReq.SecretRef.Name,
					Key:  envReq.SecretRef.Key,
				},
			}
		} else {
			newEnvVar.Value = envReq.Value
		}

		envMap[envReq.Key] = newEnvVar
	}

	// Convert map back to slice
	newEnvVars := make([]choreoapis.EnvVar, 0, len(envMap))
	for _, envVar := range envMap {
		newEnvVars = append(newEnvVars, envVar)
	}
	componentDeployment.Spec.ConfigurationOverrides.Env = newEnvVars

	// Merge file configurations
	fileMap := make(map[string]choreoapis.FileVar)
	for _, fileVar := range componentDeployment.Spec.ConfigurationOverrides.Files {
		fileMap[fileVar.Key] = fileVar
	}

	for _, fileReq := range req.Files {
		newFileVar := choreoapis.FileVar{
			Key:       fileReq.Key,
			MountPath: fileReq.MountPath,
		}

		// Check if it's a secret reference or direct value
		if fileReq.SecretRef != nil {
			newFileVar.ValueFrom = &choreoapis.EnvVarValueFrom{
				SecretRef: &choreoapis.SecretKeyRef{
					Name: fileReq.SecretRef.Name,
					Key:  fileReq.SecretRef.Key,
				},
			}
		} else if fileReq.Content != "" {
			// Decode base64 content before storing
			decodedContent, err := base64.StdEncoding.DecodeString(fileReq.Content)
			if err != nil {
				s.logger.Error("Failed to decode base64 file content", "error", err, "key", fileReq.Key)
				return nil, fmt.Errorf("failed to decode base64 content for file '%s': %w", fileReq.Key, err)
			}
			newFileVar.Value = string(decodedContent)
		}

		fileMap[fileReq.Key] = newFileVar
	}

	// Convert map back to slice
	newFileVars := make([]choreoapis.FileVar, 0, len(fileMap))
	for _, fileVar := range fileMap {
		newFileVars = append(newFileVars, fileVar)
	}
	componentDeployment.Spec.ConfigurationOverrides.Files = newFileVars

	// Update the ComponentDeployment
	if err := s.k8sClient.Update(ctx, componentDeployment); err != nil {
		s.logger.Error("Failed to update ComponentDeployment", "error", err)
		return nil, fmt.Errorf("failed to update ComponentDeployment: %w", err)
	}

	s.logger.Debug("Upserted configurations successfully",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)

	// Return the updated configurations
	return s.GetConfigurations(ctx, orgName, projectName, componentName, envName)
}

// DeleteConfigurations deletes configurations for a component in a specific environment
func (s *ConfigurationService) DeleteConfigurations(ctx context.Context, orgName, projectName, componentName, envName string, keys []string) error {
	s.logger.Debug("Deleting configurations",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
		"keys", keys,
	)

	// Get ComponentDeployment
	componentDeploymentName := fmt.Sprintf("%s-%s", componentName, envName)
	componentDeployment := &choreoapis.ComponentDeployment{}
	err := s.k8sClient.Get(ctx, types.NamespacedName{
		Name:      componentDeploymentName,
		Namespace: orgName,
	}, componentDeployment)

	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// ComponentDeployment doesn't exist - nothing to delete
			s.logger.Debug("ComponentDeployment not found, nothing to delete",
				"org", orgName,
				"project", projectName,
				"component", componentName,
				"environment", envName,
			)
			return nil
		}
		s.logger.Error("Failed to get ComponentDeployment", "error", err)
		return fmt.Errorf("failed to get ComponentDeployment: %w", err)
	}

	if componentDeployment.Spec.ConfigurationOverrides == nil {
		// No configurations to delete
		return nil
	}

	if len(keys) == 0 {
		// Delete all configurations
		componentDeployment.Spec.ConfigurationOverrides.Env = []choreoapis.EnvVar{}
		componentDeployment.Spec.ConfigurationOverrides.Files = []choreoapis.FileVar{}
	} else {
		// Delete specific keys
		keySet := make(map[string]bool)
		for _, key := range keys {
			keySet[key] = true
		}

		// Filter environment variables
		newEnvVars := make([]choreoapis.EnvVar, 0)
		for _, envVar := range componentDeployment.Spec.ConfigurationOverrides.Env {
			if !keySet[envVar.Key] {
				newEnvVars = append(newEnvVars, envVar)
			}
		}
		componentDeployment.Spec.ConfigurationOverrides.Env = newEnvVars

		// Filter file configurations
		newFileVars := make([]choreoapis.FileVar, 0)
		for _, fileVar := range componentDeployment.Spec.ConfigurationOverrides.Files {
			if !keySet[fileVar.Key] {
				newFileVars = append(newFileVars, fileVar)
			}
		}
		componentDeployment.Spec.ConfigurationOverrides.Files = newFileVars
	}

	// Update the ComponentDeployment
	if err := s.k8sClient.Update(ctx, componentDeployment); err != nil {
		s.logger.Error("Failed to update ComponentDeployment", "error", err)
		return fmt.Errorf("failed to update ComponentDeployment: %w", err)
	}

	s.logger.Debug("Deleted configurations successfully",
		"org", orgName,
		"project", projectName,
		"component", componentName,
		"environment", envName,
	)

	return nil
}
