# protonpass-server

Read [Proton Pass](https://proton.me/pass) secrets from Kubernetes with
[External Secrets Operator](https://external-secrets.io) (ESO).

protonpass-server exposes your Proton Pass items to ESO through its generic `webhook` provider. You
write an `ExternalSecret` that points at an item and a field, and ESO keeps a regular Kubernetes
`Secret` in sync with it. Under the hood it runs the official
[`pass-cli`](https://github.com/protonpass/pass-cli), isolated so that the component reachable from
the network never holds your Proton credentials.

- **Read-only**: secrets can be read, never created, changed or deleted in Proton Pass.
- **Isolated**: the Proton session lives in a container with no network port.
- **Plug and play**: one Helm chart; the only thing to provide is a Proton Pass personal access
  token.

## How it works

The chart deploys one Pod with two containers, the **gateway** and the **broker**, each running
under its own user ID.

The **gateway** is the only part reachable over the network. ESO calls it over HTTPS with a bearer
token, asking for an item reference such as `SHARE_ID/ITEM_ID` and a field such as `password`. The
gateway checks the token in constant time, validates the reference, and forwards the request to the
broker. It has no Proton credential, no access to the Proton session, and does not even contain
`pass-cli`: its container image only holds the gateway binary.

The **broker** holds the Proton Pass session. At startup it logs in with a personal access token,
then listens on a unix socket placed in a volume shared with the gateway only. The kernel tells the
broker which user ID opened each connection, and the broker drops every connection that does not
come from the gateway's user ID. For each request it runs exactly one command,
`pass-cli item view`, with no shell, a minimal environment, a timeout, and a size limit on its output.
When a field is requested, it returns `{"value": "..."}`, which ESO reads with the JSON path
`$.value`.

If the gateway is compromised, an attacker can ask for items the token can read, but cannot steal
the personal access token or the session, cannot run anything else through `pass-cli`, and cannot
write to Proton Pass. Limiting the token to a dedicated vault with the `viewer` role keeps that
exposure to the secrets Kubernetes needs anyway.

Around this core:

- TLS between ESO and the gateway uses a private CA created by the chart with
  [cert-manager](https://cert-manager.io). A public CA could not sign the gateway's internal
  `.svc` name, and ESO only trusts this CA for this store.
- A NetworkPolicy only lets ESO's namespace reach the Pod, and only lets the Pod out to DNS and to
  HTTPS (the Proton API).
- Both containers run as non-root, with read-only root filesystems, no Linux capabilities and the
  default seccomp profile. The Proton session and the socket live in memory, never on the node's
  disk.
- The gateway reports itself not ready when the broker has no valid Proton session, so Kubernetes
  stops sending it traffic. Both binaries finish running requests on shutdown.

## Requirements

- Kubernetes with [External Secrets Operator](https://external-secrets.io) (v1 API, tested with
  2.11).
- [cert-manager](https://cert-manager.io), or your own TLS Secret (see the
  [chart documentation](charts/protonpass-server/README.md)).
- A Proton Pass account, and a personal access token. Prefer a token limited to a dedicated vault
  with the `viewer` role:

  ```sh
  pass-cli vault create --name k8s
  pass-cli personal-access-token create --name k8s --expiration 1y
  pass-cli personal-access-token access grant --personal-access-token-name k8s \
    --vault-name k8s --role viewer
  ```

## Installing with Helm

Store the personal access token (`pst_<token>::<key>`) in a Secret, then install the chart from
the GitHub container registry:

```sh
kubectl create namespace protonpass
kubectl -n protonpass create secret generic protonpass-pat --from-file=pat=./pat

helm install protonpass oci://ghcr.io/hugz6/charts/protonpass-server \
  --version <version> -n protonpass
```

The chart creates the private CA, the gateway certificate, the token shared between ESO and the
gateway, and a `ClusterSecretStore` named `protonpass`. Check that everything is up:

```sh
kubectl -n protonpass get pods            # 2/2 Running
kubectl get clustersecretstore protonpass # Valid
```

All settings are listed in the [chart documentation](charts/protonpass-server/README.md).

## Reading a secret

Find the IDs of the vault and of the item:

```sh
pass-cli vault list --output json
pass-cli item list --share-id <SHARE_ID> --output json
```

Then reference them in an `ExternalSecret`: `remoteRef.key` is `SHARE_ID/ITEM_ID`, and
`remoteRef.property` the name of the field.

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
        key: <SHARE_ID>/<ITEM_ID>
        property: password
```

ESO creates the `database` Secret and refreshes it every hour. To pick up a change in Proton Pass
right away:

```sh
kubectl annotate externalsecret database force-sync=$(date +%s) --overwrite
```

## Development

The development environment is a Nix flake (Go, `pass-cli`, Helm, kubectl, kind, helm-docs):

```sh
direnv allow        # or: nix develop
go test -race ./...
go build -o bin/ ./cmd/...
docker build -f docker/gateway.Dockerfile -t protonpass-gateway:dev .
docker build -f docker/broker.Dockerfile  -t protonpass-broker:dev .
```

Pushing a `vX.Y.Z` tag runs the tests, then publishes both images (amd64 and arm64) and the chart
to `ghcr.io/hugz6`.

## License

Copyright 2026 hugz6.

Licensed under the [Apache License, Version 2.0](LICENSE). You may use, modify and redistribute
this software, including commercially, provided that you keep the copyright notice, include the
[`NOTICE`](NOTICE) file crediting the author in any redistribution, and state the changes you made.
