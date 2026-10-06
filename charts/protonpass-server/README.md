# protonpass-server

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Read Proton Pass secrets from External Secrets Operator, through a gateway and a pass-cli broker isolated in one Pod.

## How it works

```
ESO ──HTTPS──▶ gateway ──HTTP over a unix socket──▶ broker ──▶ pass-cli ──▶ Proton
               token, TLS                          personal access token
```

Both run in one Pod, in two containers with distinct UIDs:

- the **gateway** is the only network-facing part. It checks ESO's bearer token, validates the
  requested reference and forwards it. It holds no Proton credential and cannot run pass-cli;
- the **broker** holds the Proton Pass session. It listens on a unix socket only reachable by the
  gateway's UID, and only knows how to read an item.

Each Secret is mounted only in the container that uses it, the root filesystems are read-only, and
a NetworkPolicy only lets ESO in.

## Prerequisites

- [External Secrets Operator](https://external-secrets.io) v1 API (tested with 2.11).
- A Proton Pass **personal access token**, ideally limited to a dedicated vault with the `viewer`
  role:
  ```sh
  pass-cli personal-access-token create --name k8s --expiration 1y
  pass-cli personal-access-token access grant --personal-access-token-name k8s \
    --vault-name k8s --role viewer
  ```
- A TLS certificate for the gateway: either [cert-manager](https://cert-manager.io)
  (`tls.certManager.enabled`), or a `kubernetes.io/tls` Secret with `tls.crt`, `tls.key` and
  `ca.crt`.

## Installing

Create the Secrets the chart refers to. The labels are required by ESO's webhook provider:

```sh
kubectl create namespace protonpass

kubectl -n protonpass create secret generic protonpass-pat --from-file=pat=./pat
kubectl -n protonpass create secret generic protonpass-gateway-token \
  --from-literal=token="$(head -c 32 /dev/urandom | base64)"
kubectl -n protonpass label secret protonpass-gateway-token external-secrets.io/type=webhook
```

Then install the chart, here with cert-manager:

```sh
helm install protonpass oci://ghcr.io/hugz6/charts/protonpass-server \
  --version 0.1.0 -n protonpass \
  --set tls.certManager.enabled=true --set tls.certManager.issuerRef.name=<issuer>
```

Without cert-manager, the TLS Secret must also carry the label `external-secrets.io/type=webhook`.

## Reading a secret

`remoteRef.key` is `SHARE_ID/ITEM_ID`, `remoteRef.property` the field name:

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: database
spec:
  refreshInterval: 1h
  secretStoreRef:
    kind: ClusterSecretStore
    name: protonpass
  target:
    name: database
  data:
    - secretKey: password
      remoteRef:
        key: SHARE_ID/ITEM_ID
        property: password
```

The IDs come from `pass-cli vault list --output json` and
`pass-cli item list --share-id SHARE_ID --output json`.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `secret does not contain needed label 'external-secrets.io/type: webhook'` | the token or TLS Secret lacks the label |
| `Secret does not exist` | the gateway answered 404: check its logs and the `remoteRef.key` format |
| Pod `1/2` ready (gateway not ready) | the broker has no valid Proton session: check the broker logs and the personal access token |
| `Personal access token ... must have exactly 64 characters` | the `pat` Secret does not hold a valid token |

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| broker.image.pullPolicy | string | `"IfNotPresent"` | Broker image pull policy. |
| broker.image.repository | string | `"ghcr.io/hugz6/protonpass-broker"` | Broker image repository (contains pass-cli). |
| broker.image.tag | string | `.Chart.AppVersion` | Broker image tag. |
| broker.maxConcurrent | int | `4` | Maximum pass-cli calls running at the same time. Extra requests wait, then get a 503. |
| broker.timeout | string | `"30s"` | Timeout of one pass-cli call. |
| broker.uid | int | `10001` | UID of the broker container. |
| eso.clusterSecretStore.create | bool | `true` | Create a `ClusterSecretStore` pointing at the gateway. |
| eso.clusterSecretStore.jsonPath | string | `"$.value"` | Where ESO reads the value. The broker answers `{"value": "..."}` when a field (`remoteRef.property`) is set. |
| eso.clusterSecretStore.name | string | `"protonpass"` | Name of the `ClusterSecretStore`. |
| eso.namespace | string | `"external-secrets"` | Namespace of External Secrets Operator: the only one allowed to reach the gateway (NetworkPolicy). |
| fsGroup | int | `10000` | Group shared by both containers. It owns the socket volume, so the gateway can reach the broker. |
| gateway.image.pullPolicy | string | `"IfNotPresent"` | Gateway image pull policy. |
| gateway.image.repository | string | `"ghcr.io/hugz6/protonpass-gateway"` | Gateway image repository. |
| gateway.image.tag | string | `.Chart.AppVersion` | Gateway image tag. |
| gateway.timeout | string | `"35s"` | Timeout of one broker call. Must outlast `broker.timeout`, or the gateway gives up before the answer. |
| gateway.uid | int | `10002` | UID of the gateway container. The broker only accepts connections from this UID. |
| resources.broker | object | `{"limits":{"memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resources of the broker container (pass-cli runs here). |
| resources.gateway | object | `{"limits":{"memory":"64Mi"},"requests":{"cpu":"10m","memory":"32Mi"}}` | Resources of the gateway container. |
| secrets.pat | string | `"protonpass-pat"` | Name of an existing Secret holding the Proton Pass personal access token (`pst_<token>::<key>`) under the key `pat`. Mounted in the broker only. |
| secrets.token | string | `"protonpass-gateway-token"` | Name of an existing Secret holding the bearer token ESO sends to the gateway, under the key `token`. Mounted in the gateway only. ESO only reads it if it carries the label `external-secrets.io/type=webhook`. |
| tls.certManager.enabled | bool | `false` | Create the TLS Secret with a cert-manager `Certificate` instead of providing it. |
| tls.certManager.issuerRef.kind | string | `"Issuer"` | `Issuer` or `ClusterIssuer`. |
| tls.certManager.issuerRef.name | string | `""` | Issuer signing the certificate. Required when `tls.certManager.enabled`. |
| tls.secretName | string | `"protonpass-gateway-tls"` | Name of the TLS Secret of the gateway (`tls.crt`, `tls.key`, `ca.crt`). ESO trusts `ca.crt`, and only reads it if it carries the label `external-secrets.io/type=webhook` (set automatically with cert-manager). |
