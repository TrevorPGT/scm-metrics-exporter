# Trust a private CA

Collect from a GitHub Enterprise Server or self-managed GitLab instance whose TLS
certificate was issued by a private CA. Without this, discovery and collection
fail with `x509: certificate signed by unknown authority`.

Both binaries are Go and read the system trust store, which `SSL_CERT_DIR` can
extend. You mount the CA into the two places that call the SCM API: the operator
(repository discovery) and the collection Jobs.

For the full field list, see the [CRD reference](crd-reference.md).

## Prerequisites

- The CA certificate in PEM form.
- The operator installed from the Helm chart, and a CR you are about to create or
  already reconciling (see the [README](../README.md)).

## Step 1: Store the CA in each namespace

Pods can only mount a Secret or ConfigMap from their own namespace. Create the
CA in the operator's namespace, and in the CR's namespace if that is a different
one:

```sh
kubectl create secret generic private-ca --namespace scm-system \
  --from-file=ca.crt=./private-ca.pem
```

A ConfigMap works too. If you already distribute the CA with a tool such as
trust-manager, point the volumes below at the object it writes instead.

## Step 2: Mount it into the operator

Set these chart values and upgrade the release:

```yaml
extraVolumes:
  - name: private-ca
    secret:
      secretName: private-ca
extraVolumeMounts:
  - name: private-ca
    mountPath: /etc/scm/ca
    readOnly: true
extraEnv:
  - name: SSL_CERT_DIR
    value: /etc/ssl/certs:/etc/scm/ca
```

`SSL_CERT_DIR` replaces Go's default certificate directories, so keep
`/etc/ssl/certs` in it, or the public CAs stop being trusted.

## Step 3: Mount it into the collection Jobs

Add the same three settings to the CR's spec:

```yaml
apiVersion: scm.jalet.io/v1alpha1
kind: GitLabMetricsExporter
metadata:
  name: acme
  namespace: scm-system
spec:
  baseURL: https://gitlab.example.internal
  # ... the rest of your spec ...
  volumes:
    - name: private-ca
      secret:
        secretName: private-ca
  volumeMounts:
    - name: private-ca
      mountPath: /etc/scm/ca
      readOnly: true
  env:
    - name: SSL_CERT_DIR
      value: /etc/ssl/certs:/etc/scm/ca
```

The operator adds these after its own volumes and variables. Jobs dispatched
from the next discovery cycle on carry them.

## Verify

1. The CR reports `Ready=True` with repositories discovered:

   ```sh
   kubectl get gitlabmetricsexporter acme -n scm-system \
     -o jsonpath='{.status.conditions[?(@.type=="Ready")].status} {.status.discoveredRepositories}'
   ```

2. A collection Job completes:

   ```sh
   kubectl get jobs -n scm-system -l app.kubernetes.io/instance=acme
   ```

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| CR not `Ready`, operator logs `x509: certificate signed by unknown authority` | The operator does not have the CA | Check Step 2: the volume, mount and `SSL_CERT_DIR` on the operator Deployment |
| Discovery succeeds but Jobs fail with the same error | The Jobs do not have the CA | Check Step 3, and that the Secret exists in the CR's namespace |
| Calls to public endpoints (for example github.com) start failing | `SSL_CERT_DIR` lost `/etc/ssl/certs` | Set it to `/etc/ssl/certs:<your mount path>` |
| Job pods stuck in `ContainerCreating` | The Secret or ConfigMap is missing in the CR's namespace | Create it there (Step 1) |
