---
title: Getting Started
description: Send your first error to BugBarn in under five minutes.
---

# Getting Started

Get your first error into BugBarn in under five minutes.

## 1. Create a project

Log into your BugBarn dashboard and create a new project. Copy the **API key** from the project settings page. You will also need the **ingest endpoint** -- this is your BugBarn instance URL followed by `/api/v1/events` (for example `https://bugbarn.example.com/api/v1/events`).

## 2. Install an SDK

Pick the SDK for your language and install it:

**Go**

```bash
go get github.com/wiebe-xyz/bugbarn-go
```

**TypeScript / JavaScript** (Node.js and browser)

```bash
npm install @bugbarn/typescript
```

**Python**

```bash
pip install bugbarn-python
```

**PHP**

```bash
composer require bugbarn/bugbarn-php
```

## 3. Initialise the SDK

### Go

```go
import bugbarn "github.com/wiebe-xyz/bugbarn-go"

func main() {
    bugbarn.Init(bugbarn.Options{
        APIKey:   "your-api-key",
        Endpoint: "https://bugbarn.example.com/api/v1/events",
    })
    defer bugbarn.Shutdown(2 * time.Second)

    // your application code ...
}
```

### TypeScript

```typescript
import * as bugbarn from "@bugbarn/typescript";

bugbarn.init({
  apiKey: "your-api-key",
  endpoint: "https://bugbarn.example.com/api/v1/events",
});
```

### Python

```python
import bugbarn

bugbarn.init(
    api_key="your-api-key",
    endpoint="https://bugbarn.example.com/api/v1/events",
)
```

### PHP

```php
use BugBarn\Client;

Client::init(
    apiKey:   'your-api-key',
    endpoint: 'https://bugbarn.example.com/api/v1/events',
);
```

## 4. Capture an error

Trigger a test error to confirm everything is wired up:

### Go

```go
bugbarn.CaptureError(errors.New("Hello from BugBarn!"))
```

### TypeScript

```typescript
await bugbarn.captureException(new Error("Hello from BugBarn!"));
```

### Python

```python
bugbarn.capture_exception(Exception("Hello from BugBarn!"))
```

### PHP

```php
try {
    throw new \RuntimeException("Hello from BugBarn!");
} catch (\Throwable $e) {
    Client::captureException($e);
}
```

## 5. Check the dashboard

Open your BugBarn dashboard. You should see a new issue appear within a few seconds. If it does not show up, verify that:

- The API key is correct
- The endpoint URL is reachable from your application
- There are no network firewalls blocking outbound HTTPS requests

## Next steps

- **[Go SDK](/bugbarn/docs/sdks/go)** -- middleware, user context, release tagging
- **[TypeScript SDK](/bugbarn/docs/sdks/typescript)** -- uncaught exception handling, source maps, breadcrumbs
- **[Python SDK](/bugbarn/docs/sdks/python)** -- excepthook integration, breadcrumbs, user context
- **[PHP SDK](/bugbarn/docs/sdks/php)** -- error handlers, breadcrumbs, user context
- **[HTTP API](/bugbarn/docs/sdks/http)** -- send events from any language using plain HTTP
