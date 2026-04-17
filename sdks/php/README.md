# BugBarn PHP SDK

Lightweight error reporting for self-hosted [BugBarn](https://github.com/webwiebe/bugbarn) instances.

## Requirements

- PHP 8.1+
- `ext-curl`
- `ext-json`

## Installation

```sh
composer require bugbarn/bugbarn-php
```

Or from source:

```sh
cd sdks/php && composer install
```

## Usage

### Initialize

```php
use BugBarn\Client;

Client::init(
    apiKey:          'bb_live_<your-api-key>',
    endpoint:        'https://bugbarn.example.com/api/v1/events',
    installHandlers: true,   // install set_exception_handler + shutdown fatal catcher
    projectSlug:     'my-app',
);
```

### Manual capture

```php
try {
    riskyOperation();
} catch (\Throwable $e) {
    Client::captureException($e, ['environment' => 'production']);
}
```

### Flush / shutdown

Events are flushed automatically via `register_shutdown_function`. To flush
explicitly (e.g. before a long-running loop sleeps):

```php
Client::flush();
```

To detach the transport and free resources:

```php
Client::shutdown();
```

## Transport model

Events are buffered in an in-process queue (default cap: 256). The queue is
flushed at script end via a registered shutdown function, or explicitly via
`Client::flush()`. Each event is sent synchronously with a 2-second curl
timeout so a slow or unavailable BugBarn instance never blocks the calling
process for more than 2 s per queued event.

On PHP-FPM, call `fastcgi_finish_request()` before `Client::flush()` to send
the response to the client before the events are delivered.

## Uncaught exception handler

When `installHandlers: true`:

- `set_exception_handler` is installed — uncaught `Throwable`s are captured
  and the queue is flushed before PHP's default output runs.
- A `register_shutdown_function` is installed — fatal errors (E_ERROR, parse
  errors, etc.) that bypass the exception handler are captured.

## Sample app

```sh
BUGBARN_ENDPOINT=http://localhost:8080/api/v1/events \
BUGBARN_API_KEY=bb_live_xxx \
php sample/sample.php
```
