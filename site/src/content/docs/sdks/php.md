---
title: PHP SDK
description: Integrate BugBarn error tracking into PHP applications.
---

# PHP SDK

The PHP SDK (`bugbarn/bugbarn-php`) is a lightweight client that uses cURL for event delivery. It supports automatic error/exception handlers, breadcrumbs, and user context.

## Installation

```bash
composer require bugbarn/bugbarn-php
```

Requires PHP 8.1+ with the `curl` and `json` extensions.

## Initialisation

```php
use BugBarn\Client;

Client::init(
    apiKey:          'your-api-key',
    endpoint:        'https://bugbarn.example.com/api/v1/events',
    projectSlug:     'my-app',    // optional -- route events to a specific project
    installHandlers: false,       // optional -- install exception and fatal error handlers
);
```

A shutdown function is always registered to flush queued events when the script exits.

### Options reference

| Parameter | Type | Default | Description |
|---|---|---|---|
| `apiKey` | `string` | required | Project API key |
| `endpoint` | `string` | required | BugBarn ingest URL (`/api/v1/events`) |
| `projectSlug` | `string` | `''` | Project slug for event routing |
| `installHandlers` | `bool` | `false` | Install exception handler and fatal error handler |

## Capturing errors

### captureException

Capture a `Throwable` (exceptions and errors). Returns `true` if enqueued.

```php
try {
    riskyOperation();
} catch (\Throwable $e) {
    Client::captureException($e);
}
```

### captureMessage

Capture a plain string message.

```php
Client::captureMessage('Deployment completed');
```

### Attributes

Both methods accept an associative array of attributes:

```php
Client::captureException($e, [
    'environment' => 'production',
    'service'     => 'checkout',
    'order_id'    => 42,
]);

Client::captureMessage('low disk space', [
    'disk_usage' => '95%',
]);
```

## Automatic error handlers

Set `installHandlers: true` to automatically capture:

- **Uncaught exceptions** -- via `set_exception_handler`
- **Fatal errors** -- via a `register_shutdown_function` that checks `error_get_last()` for `E_ERROR`, `E_PARSE`, `E_CORE_ERROR`, `E_COMPILE_ERROR`, and `E_USER_ERROR`

```php
Client::init(
    apiKey:          'your-api-key',
    endpoint:        'https://bugbarn.example.com/api/v1/events',
    installHandlers: true,
);

// Uncaught exceptions are now captured automatically
throw new \RuntimeException('this will be reported');
```

Events are flushed immediately after capture in the error handlers, so they are sent even if the process is about to terminate.

## Breadcrumbs

Record a trail of events leading up to an error:

```php
Client::addBreadcrumb(
    category: 'http',
    message:  'GET /api/users',
    level:    'info',
    data:     ['status_code' => 200],
);
```

Clear breadcrumbs:

```php
Client::clearBreadcrumbs();
```

Up to 100 breadcrumbs are kept. When the limit is reached, the oldest is dropped.

## User context

Attach user information to all subsequent events:

```php
Client::setUser(id: 'u_123', email: 'jane@example.com', username: 'jane');
```

Clear user context:

```php
Client::clearUser();
```

## Flush and shutdown

### flush

Deliver all queued events synchronously.

```php
Client::flush();
```

Flush is called automatically when the PHP script exits.

### shutdown

Flush all events and reset the SDK state.

```php
Client::shutdown();
```

## Framework integration

### Laravel

Register BugBarn in a service provider and use Laravel's exception handler:

```php
// app/Providers/BugBarnServiceProvider.php
namespace App\Providers;

use BugBarn\Client;
use Illuminate\Support\ServiceProvider;

class BugBarnServiceProvider extends ServiceProvider
{
    public function boot(): void
    {
        Client::init(
            apiKey:      config('services.bugbarn.api_key'),
            endpoint:    config('services.bugbarn.endpoint'),
            projectSlug: config('services.bugbarn.project', ''),
        );
    }
}

// app/Exceptions/Handler.php
namespace App\Exceptions;

use BugBarn\Client;
use Illuminate\Foundation\Exceptions\Handler as ExceptionHandler;
use Throwable;

class Handler extends ExceptionHandler
{
    public function report(Throwable $e): void
    {
        Client::captureException($e);
        parent::report($e);
    }
}
```

### Symfony

Register as a Symfony event listener on kernel exceptions:

```php
// src/EventListener/BugBarnListener.php
namespace App\EventListener;

use BugBarn\Client;
use Symfony\Component\HttpKernel\Event\ExceptionEvent;

class BugBarnListener
{
    public function onKernelException(ExceptionEvent $event): void
    {
        Client::captureException($event->getThrowable(), [
            'path' => $event->getRequest()->getPathInfo(),
        ]);
    }
}
```

```yaml
# config/services.yaml
services:
  App\EventListener\BugBarnListener:
    tags:
      - { name: kernel.event_listener, event: kernel.exception }
```

## Full example

```php
<?php

require __DIR__ . '/vendor/autoload.php';

use BugBarn\Client;

Client::init(
    apiKey:          getenv('BUGBARN_API_KEY'),
    endpoint:        getenv('BUGBARN_ENDPOINT'),
    installHandlers: true,
    projectSlug:     'my-php-app',
);

Client::setUser(id: 'u_42', email: 'dev@example.com');

Client::addBreadcrumb(
    category: 'startup',
    message:  'Application initialized',
    level:    'info',
);

try {
    throw new \RuntimeException('Something went wrong');
} catch (\Throwable $e) {
    Client::captureException($e, [
        'environment' => 'local',
        'service'     => 'sample',
    ]);
}

Client::shutdown();
```
