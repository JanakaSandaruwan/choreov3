/*
 * Copyright (c) 2025, WSO2 Inc. (http://www.wso2.org) All Rights Reserved.
 *
 * WSO2 Inc. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package endpoint

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	choreov1 "github.com/wso2-enterprise/choreo-cp-declarative-api/api/v1"
	"github.com/wso2-enterprise/choreo-cp-declarative-api/internal/controller"
	"github.com/wso2-enterprise/choreo-cp-declarative-api/internal/controller/endpoint/integrations/kubernetes"
	"github.com/wso2-enterprise/choreo-cp-declarative-api/internal/dataplane"
	"github.com/wso2-enterprise/choreo-cp-declarative-api/internal/slice"
)

// Reconciler reconciles a Endpoint object
type Reconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Endpoint object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.0/pkg/reconcile
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get Endpoint CR
	e := &choreov1.Endpoint{}
	old := e.DeepCopy()

	if err := r.Get(ctx, req.NamespacedName, e); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if e.Labels == nil {
		logger.Info("Endpoint labels not set.")
		return ctrl.Result{}, nil
	}

	// do we add finalizer only if there are dependent crs?
	resourceHandlers := r.makeExternalResourceHandlers()
	endpointCtx, err := r.makeEndpointContext(ctx, e)
	if err != nil {
		logger.Error(err, "Failed to create endpoint context")
		r.recorder.Eventf(e, corev1.EventTypeWarning, "ContextResolutionFailed",
			"Context resolution failed: %v", err)
		return ctrl.Result{}, controller.IgnoreHierarchyNotFoundError(err)
	}

	if e.DeletionTimestamp.IsZero() {
		if err := r.addFinalizer(ctx, e); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		if err := r.removeFinalizer(ctx, e); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.removeExternalResources(ctx, resourceHandlers, endpointCtx); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err = r.reconcileExternalResources(ctx, resourceHandlers, endpointCtx); err != nil {
		// TODO Verify if this is necessary
		base := client.StrategicMergeFrom(e.DeepCopy())
		meta.SetStatusCondition(&e.Status.Conditions, NewEndpointReadyCondition(e.Generation, false, err.Error()))
		logger.Error(err, "failed to reconcile external resources")
		r.recorder.Eventf(e, corev1.EventTypeWarning, "ExternalResourceReconciliationFailed",
			"External resource reconciliation failed: %s", err)
		if err := r.Client.Patch(ctx, e, base); err != nil {
			return ctrl.Result{}, fmt.Errorf("%w, failed to patch endpoint ready condition", err)
		}
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&e.Status.Conditions, NewEndpointReadyCondition(e.Generation, true, ""))
	e.Status.Address = kubernetes.MakeAddress(endpointCtx.Component.Name, endpointCtx.Environment.Name, endpointCtx.Component.Spec.Type, endpointCtx.Endpoint.Spec.Service.BasePath)
	if e.Status.Address != old.Status.Address ||
		controller.NeedConditionUpdate(old.Status.Conditions, e.Status.Conditions) {
		if err := r.Status().Update(ctx, e); err != nil {
			logger.Error(err, "Failed to update Endpoint status")
			return ctrl.Result{}, err
		}
	}

	oldReadyCondition := meta.IsStatusConditionTrue(old.Status.Conditions, ConditionReady.String())
	newReadyCondition := meta.IsStatusConditionTrue(e.Status.Conditions, ConditionReady.String())

	// Emit an event if the endpoint is transitioning to ready
	if !oldReadyCondition && newReadyCondition {
		r.recorder.Eventf(e, corev1.EventTypeNormal, "EndpointReady",
			"Endpoint is ready")
	}

	return ctrl.Result{}, nil
}

// makeEndpointContext creates a endpoint context for the given deployment by retrieving the
// parent objects that this deployment is associated with.
func (r *Reconciler) makeEndpointContext(ctx context.Context, e *choreov1.Endpoint) (*dataplane.EndpointContext, error) {
	project, err := controller.GetProject(ctx, r.Client, e)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve the project: %w", err)
	}

	component, err := controller.GetComponent(ctx, r.Client, e)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve the component: %w", err)
	}

	deploymentTrack, err := controller.GetDeploymentTrack(ctx, r.Client, e)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve the deployment track: %w", err)
	}

	environment, err := controller.GetEnvironment(ctx, r.Client, e)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve the environment: %w", err)
	}

	deployment, err := controller.GetDeployment(ctx, r.Client, e)
	if err != nil {
		return nil, fmt.Errorf("cannot retrieve the deployment: %w", err)
	}

	return &dataplane.EndpointContext{
		Project:         project,
		Component:       component,
		DeploymentTrack: deploymentTrack,
		Deployment:      deployment,
		Environment:     environment,
		Endpoint:        e,
	}, nil
}

func (r *Reconciler) makeExternalResourceHandlers() []dataplane.ResourceHandler[dataplane.EndpointContext] {
	// Define the resource handlers for the external resources
	resourceHandlers := []dataplane.ResourceHandler[dataplane.EndpointContext]{
		kubernetes.NewHTTPRouteHandler(r.Client),
	}

	return resourceHandlers
}

// reconcileExternalResources reconciles the provided external resources based on the deployment context.
func (r *Reconciler) reconcileExternalResources(
	ctx context.Context,
	resourceHandlers []dataplane.ResourceHandler[dataplane.EndpointContext],
	endpointCtx *dataplane.EndpointContext) error {
	handlerNameLogKey := "resourceHandler"
	for _, resourceHandler := range resourceHandlers {
		logger := log.FromContext(ctx).WithValues(handlerNameLogKey, resourceHandler.Name())
		// Delete the external resource if it is not configured
		if !resourceHandler.IsRequired(endpointCtx) {
			if err := resourceHandler.Delete(ctx, endpointCtx); err != nil {
				logger.Error(err, "Error deleting external resource")
				return err
			}
			// No need to reconcile the external resource if it is not required
			logger.Info("Deleted external resource")
			continue
		}

		// Check if the external resource exists
		currentState, err := resourceHandler.GetCurrentState(ctx, endpointCtx)
		if err != nil {
			logger.Error(err, "Error retrieving current state of the external resource")
			return err
		}
		exists := currentState != nil
		if !exists {
			// Create the external resource if it does not exist
			if err := resourceHandler.Create(ctx, endpointCtx); err != nil {
				logger.Error(err, "Error creating external resource")
				return err
			}
		} else {
			// Update the external resource if it exists
			if err := resourceHandler.Update(ctx, endpointCtx, currentState); err != nil {
				logger.Error(err, "Error updating external resource")
				return err
			}
		}

		logger.Info("Reconciled external resource")
	}

	return nil
}

func (r *Reconciler) removeExternalResources(ctx context.Context, resourceHandlers []dataplane.ResourceHandler[dataplane.EndpointContext], endpointCtx *dataplane.EndpointContext) error {
	for _, rh := range resourceHandlers {
		state, err := rh.GetCurrentState(ctx, endpointCtx)
		if err != nil {
			return fmt.Errorf("error retrieving current state of the external resource: %w", err)
		}
		if state != nil {
			if err := rh.Delete(ctx, endpointCtx); err != nil {
				return fmt.Errorf("error deleting endpoints external resource: %w", err)
			}
		}
	}
	return nil
}

func (r *Reconciler) addFinalizer(ctx context.Context, e *choreov1.Endpoint) error {
	if !slice.ContainsString(e.Finalizers, choreov1.EndpointFinalizer) {
		base := client.MergeFrom(e.DeepCopy())
		e.Finalizers = append(e.Finalizers, choreov1.EndpointFinalizer)
		if err := r.Client.Patch(ctx, e, base); err != nil {
			return fmt.Errorf("failed to add finalizer to endpoint %s: %w", e.Name, err)
		}
	}
	return nil
}

func (r *Reconciler) removeFinalizer(ctx context.Context, e *choreov1.Endpoint) error {
	if slice.ContainsString(e.Finalizers, choreov1.EndpointFinalizer) {
		base := client.MergeFrom(e.DeepCopy())
		e.Finalizers = slice.RemoveString(e.Finalizers, choreov1.EndpointFinalizer)
		if err := r.Client.Patch(ctx, e, base); err != nil {
			return fmt.Errorf("failed to add finalizer to endpoint %s: %w", e.Name, err)
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.recorder == nil {
		r.recorder = mgr.GetEventRecorderFor("endpoint-controller")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&choreov1.Endpoint{}).
		Named("endpoint").
		Complete(r)
}
