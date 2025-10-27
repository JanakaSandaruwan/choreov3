# Comprehensive Component Rendering Example

This example demonstrates the complete end-to-end workflow of the OpenChoreo component rendering system with the new `MetadataContext` architecture.

## Overview

This example shows:
- Component type definitions with templated resources
- Addon composition with creates and patches
- Environment-specific overrides
- Automatic name/namespace generation
- Full pipeline rendering with the EnvSettings controller

## Architecture

```
ComponentEnvSnapshot ──┐
                       │
                       ├──> EnvSettings Controller ──> Pipeline.Render()
                       │                                     │
EnvSettings ───────────┘                                     │
                                                             ↓
                                                      Release (with rendered resources)
```

## Components

### 1. ComponentTypeDefinition (`1-componenttypedefinition.yaml`)

Defines a `web-service` component type that creates:
- **Deployment**: With configurable replicas, resources, and container image
- **Service**: ClusterIP service exposing the deployment

Key features:
- Uses `${metadata.name}` and `${metadata.namespace}` for computed names
- Uses `${metadata.labels}` and `${metadata.podSelectors}` for labels/selectors
- Uses `${workload.containers["app"].image}` for the container image
- Supports environment-specific resource overrides

### 2. Addon (`2-addon.yaml`)

Defines a `persistent-volume` addon that:
- **Creates**: A PersistentVolumeClaim
- **Patches**: Adds a volume and volumeMount to the Deployment

Key features:
- Parameterized volume name, mount path, and container name
- Environment-specific size and storage class overrides
- Uses `${metadata.name}-${addon.instanceId}` for PVC naming

### 3. Component (`3-component.yaml`)

Defines a `demo-api` component that:
- Uses the `web-service` component type
- Sets parameters for replicas, resources, and port
- Attaches the `persistent-volume` addon with instance ID `data-storage`

### 4. Workload (`4-workload.yaml`)

Represents the build output with:
- Container image: `nginx:1.25-alpine`
- This would normally be created by a Build controller

### 5. ComponentEnvSnapshot (`5-componentenvsnapshot.yaml`)

An immutable snapshot combining:
- Component definition
- ComponentTypeDefinition
- Workload (with built image)
- All referenced Addons

This is the input to the rendering pipeline.

### 6. EnvSettings (`6-envsettings.yaml`)

Environment-specific overrides for the `development` environment:
- Reduces resource requests/limits for cost savings
- Changes PVC size from 20Gi → 5Gi
- Changes storage class from "fast" → "standard"

## Expected Output

When the EnvSettings controller processes the ComponentEnvSnapshot with EnvSettings, it will:

1. **Build MetadataContext**:
   ```
   Name:      demo-api-development-<hash>
   Namespace: dp-demo-org-demo-project-development-<hash>
   Labels:
     openchoreo.org/organization: demo-org
     openchoreo.org/project: demo-project
     openchoreo.org/component: demo-api
     openchoreo.org/environment: development
   PodSelectors:
     openchoreo.org/component: demo-api
     openchoreo.org/environment: development
     openchoreo.org/project: demo-project
     openchoreo.org/component-id: abc-demo-api-123
   ```

2. **Render base resources** (Deployment + Service):
   - Name and namespace use computed values from MetadataContext
   - Resources use overridden values from EnvSettings (50m CPU, 128Mi memory)

3. **Apply addon** (persistent-volume):
   - Create PVC with name: `demo-api-development-<hash>-data-storage`
   - Add volume to Deployment
   - Add volumeMount to `app` container
   - PVC size: 5Gi (overridden), storage class: standard (overridden)

4. **Create Release** with all rendered resources:
   - `deployment-demo-api-development-<hash>`
   - `service-demo-api-development-<hash>`
   - `persistentvolumeclaim-demo-api-development-<hash>-data-storage`

## Testing on Cluster

### Prerequisites

```bash
# Ensure OpenChoreo CRDs are installed
kubectl apply -f install/helm/openchoreo/crds/

# Ensure OpenChoreo controller is running
make run
```

### Step 1: Create the namespace

```bash
kubectl create namespace demo-org
```

### Step 2: Apply resources in order

```bash
# Apply in numbered order
kubectl apply -f samples/comprehensive-example/1-componenttypedefinition.yaml
kubectl apply -f samples/comprehensive-example/2-addon.yaml
kubectl apply -f samples/comprehensive-example/3-component.yaml
kubectl apply -f samples/comprehensive-example/4-workload.yaml
kubectl apply -f samples/comprehensive-example/5-componentenvsnapshot.yaml
kubectl apply -f samples/comprehensive-example/6-envsettings.yaml
```

Or apply all at once:
```bash
kubectl apply -f samples/comprehensive-example/
```

### Step 3: Verify the Release is created

```bash
# Check Release was created
kubectl get release -n demo-org

# View Release details
kubectl get release demo-api-development -n demo-org -o yaml

# Check the rendered resources
kubectl get release demo-api-development -n demo-org -o jsonpath='{.spec.resources[*].id}'
```

### Step 4: Verify EnvSettings status

```bash
# Check EnvSettings status
kubectl get envsettings -n demo-org

# View detailed status
kubectl describe envsettings demo-api-development -n demo-org
```

## Expected Rendering

The Release should contain 3 rendered resources:

### 1. Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo-api-development-<hash>
  namespace: dp-demo-org-demo-project-development-<hash>
  labels:
    openchoreo.org/organization: demo-org
    openchoreo.org/project: demo-project
    openchoreo.org/component: demo-api
    openchoreo.org/environment: development
spec:
  replicas: 2
  selector:
    matchLabels:
      openchoreo.org/component: demo-api
      openchoreo.org/environment: development
      openchoreo.org/project: demo-project
      openchoreo.org/component-id: abc-demo-api-123
  template:
    spec:
      containers:
        - name: app
          image: nginx:1.25-alpine
          resources:
            requests:
              cpu: 50m        # Overridden by EnvSettings
              memory: 128Mi   # Overridden by EnvSettings
            limits:
              cpu: 200m       # Overridden by EnvSettings
              memory: 256Mi   # Overridden by EnvSettings
          volumeMounts:       # Added by addon
            - name: app-data
              mountPath: /var/data
      volumes:                # Added by addon
        - name: app-data
          persistentVolumeClaim:
            claimName: demo-api-development-<hash>-data-storage
```

### 2. Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: demo-api-development-<hash>
  namespace: dp-demo-org-demo-project-development-<hash>
  labels:
    openchoreo.org/organization: demo-org
    openchoreo.org/project: demo-project
    openchoreo.org/component: demo-api
    openchoreo.org/environment: development
spec:
  type: ClusterIP
  selector:
    openchoreo.org/component: demo-api
    openchoreo.org/environment: development
    openchoreo.org/project: demo-project
    openchoreo.org/component-id: abc-demo-api-123
  ports:
    - name: http
      port: 80
      targetPort: 8080
```

### 3. PersistentVolumeClaim (created by addon)

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: demo-api-development-<hash>-data-storage
  namespace: dp-demo-org-demo-project-development-<hash>
  labels:
    openchoreo.org/organization: demo-org
    openchoreo.org/project: demo-project
    openchoreo.org/component: demo-api
    openchoreo.org/environment: development
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi    # Overridden by EnvSettings (was 20Gi)
  storageClassName: standard  # Overridden by EnvSettings (was "fast")
```

## Key Features Demonstrated

### 1. MetadataContext Integration
- Controller computes names and namespaces using platform conventions
- Metadata is passed to the rendering pipeline
- Templates use `${metadata.*}` expressions

### 2. Environment Overrides
- Component-level overrides (resources)
- Addon-level overrides (size, storageClass)
- Overrides are merged at render time

### 3. Addon Composition
- Creates new resources (PVC)
- Patches existing resources (Deployment volumes)
- Instance-specific configuration

### 4. Label Propagation
- Standard labels applied to all resources
- Pod selectors for Deployment/Service matching
- Component ID tracking

## Cleanup

```bash
# Delete all resources
kubectl delete -f samples/comprehensive-example/

# Or delete the namespace
kubectl delete namespace demo-org
```

## Troubleshooting

### Release not created

```bash
# Check EnvSettings status
kubectl describe envsettings demo-api-development -n demo-org

# Check controller logs
kubectl logs -n openchoreo-system deployment/openchoreo-controller
```

### Rendering errors

Look for errors in the EnvSettings conditions:

```bash
kubectl get envsettings demo-api-development -n demo-org -o jsonpath='{.status.conditions}'
```

Common issues:
- Missing required fields in snapshot
- Invalid CEL expressions in templates
- Missing addon definitions
- Schema validation failures

## Next Steps

1. **Try different environments**: Create a `production` EnvSettings with different overrides
2. **Add more addons**: Try the sidecar or emptydir addons
3. **Customize the component type**: Add more resources or template expressions
4. **Test with real builds**: Replace the Workload with actual build output
