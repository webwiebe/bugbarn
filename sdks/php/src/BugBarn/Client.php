<?php

declare(strict_types=1);

namespace BugBarn;

final class Client
{
    public const DEFAULT_ENDPOINT = '/api/v1/events';

    private static ?Transport $transport    = null;
    private static bool       $handlersInstalled = false;

    /**
     * Initialise the SDK.
     *
     * @param string $endpoint Full URL or base URL (path appended automatically if missing)
     * @param bool $installHandlers  When true, installs set_exception_handler and a
     *                               register_shutdown_function to capture fatal errors.
     */
    public static function init(
        string $apiKey,
        string $endpoint = self::DEFAULT_ENDPOINT,
        bool   $installHandlers = false,
        string $projectSlug     = '',
    ): void {
        if (!str_contains($endpoint, '/api/')) {
            $endpoint = rtrim($endpoint, '/') . self::DEFAULT_ENDPOINT;
        }

        self::$transport = new Transport(
            apiKey:      $apiKey,
            endpoint:    $endpoint,
            projectSlug: $projectSlug,
        );

        // Flush remaining events on normal script exit.
        register_shutdown_function(static function (): void {
            self::flush();
        });

        if ($installHandlers && !self::$handlersInstalled) {
            set_exception_handler(static function (\Throwable $e): void {
                self::captureThrowable($e);
                self::flush();
            });

            // Capture fatal errors (E_ERROR, parse errors, etc.) that bypass Throwable.
            register_shutdown_function(static function (): void {
                $err = error_get_last();
                if ($err === null) {
                    return;
                }
                $fatal = E_ERROR | E_PARSE | E_CORE_ERROR | E_COMPILE_ERROR | E_USER_ERROR;
                if (($err['type'] & $fatal) === 0) {
                    return;
                }
                self::captureMessage($err['message'], [
                    'error_type' => $err['type'],
                    'file'       => $err['file'],
                    'line'       => $err['line'],
                ]);
                self::flush();
            });

            self::$handlersInstalled = true;
        }
    }

    public static function setUser(string $id = '', string $email = '', string $username = ''): void
    {
        User::setUser($id, $email, $username);
    }

    public static function clearUser(): void
    {
        User::clearUser();
    }

    public static function addBreadcrumb(string $category, string $message, string $level = '', ?array $data = null): void
    {
        Breadcrumbs::add($category, $message, $level, $data);
    }

    public static function clearBreadcrumbs(): void
    {
        Breadcrumbs::clear();
    }

    /**
     * Capture a Throwable and enqueue it for delivery.
     *
     * @param array<string,mixed> $attributes
     */
    public static function captureException(\Throwable $e, array $attributes = []): bool
    {
        return self::captureThrowable($e, $attributes);
    }

    /**
     * Capture a plain string message.
     *
     * @param array<string,mixed> $attributes
     */
    public static function captureMessage(string $message, array $attributes = []): bool
    {
        if (self::$transport === null) {
            return false;
        }

        return self::$transport->enqueue(new Envelope(
            timestamp:        self::nowIso(),
            severityText:     'ERROR',
            body:             $message,
            exceptionType:    'Error',
            exceptionMessage: $message,
            stacktrace:       null,
            attributes:       $attributes,
            user:             User::getUser(),
            breadcrumbs:      Breadcrumbs::get(),
        ));
    }

    /**
     * Flush all queued events.  Called automatically on shutdown.
     */
    public static function flush(): void
    {
        self::$transport?->flush();
    }

    /**
     * Flush queued events and detach the transport.
     */
    public static function shutdown(): void
    {
        self::flush();
        self::$transport         = null;
        self::$handlersInstalled = false;
    }

    // ------------------------------------------------------------------

    /** @param array<string,mixed> $attributes */
    private static function captureThrowable(\Throwable $e, array $attributes = []): bool
    {
        if (self::$transport === null) {
            return false;
        }

        return self::$transport->enqueue(new Envelope(
            timestamp:        self::nowIso(),
            severityText:     'ERROR',
            body:             $e->getMessage(),
            exceptionType:    get_class($e),
            exceptionMessage: $e->getMessage(),
            stacktrace:       self::extractStacktrace($e),
            attributes:       $attributes,
            user:             User::getUser(),
            breadcrumbs:      Breadcrumbs::get(),
        ));
    }

    /**
     * @return array<array<string,mixed>>|null
     */
    private static function extractStacktrace(\Throwable $e): ?array
    {
        $frames = [];
        foreach ($e->getTrace() as $frame) {
            $entry = [];
            if (isset($frame['function'])) {
                $entry['function'] = $frame['function'];
            }
            if (isset($frame['file'])) {
                $entry['file']   = $frame['file'];
                $entry['module'] = basename($frame['file']);
            }
            if (isset($frame['line'])) {
                $entry['line'] = $frame['line'];
            }
            if ($entry !== []) {
                $frames[] = $entry;
            }
        }
        return $frames !== [] ? $frames : null;
    }

    private static function nowIso(): string
    {
        return (new \DateTimeImmutable())->format(\DateTimeInterface::RFC3339_EXTENDED);
    }
}
