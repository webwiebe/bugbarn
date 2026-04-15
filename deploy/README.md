# BugBarn deployment

Apply the testing or staging overlay directly:

```bash
kubectl apply -k deploy/k8s/testing
kubectl apply -k deploy/k8s/staging
```

The current MVP does not define an ingress host, so access the service with a port-forward:

```bash
kubectl -n bugbarn-testing port-forward svc/bugbarn 8080:8080
kubectl -n bugbarn-staging port-forward svc/bugbarn 8080:8080
```
