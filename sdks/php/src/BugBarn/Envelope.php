<?php

declare(strict_types=1);

namespace BugBarn;

final class Envelope
{
    private const SDK_NAME    = 'bugbarn.php';
    private const SDK_VERSION = '0.1.0';

    /**
     * @param array<array<string,mixed>>|null $stacktrace
     * @param array<string,mixed>             $attributes
     */
    public function __construct(
        public readonly string  $timestamp,
        public readonly string  $severityText,
        public readonly string  $body,
        public readonly string  $exceptionType,
        public readonly string  $exceptionMessage,
        public readonly ?array  $stacktrace = null,
        public readonly array   $attributes = [],
    ) {}

    /** @return array<string,mixed> */
    public function toPayload(): array
    {
        $exception = [
            'type'    => $this->exceptionType,
            'message' => $this->exceptionMessage,
        ];
        if ($this->stacktrace !== null) {
            $exception['stacktrace'] = $this->stacktrace;
        }

        return [
            'timestamp'    => $this->timestamp,
            'severityText' => $this->severityText,
            'body'         => $this->body,
            'exception'    => $exception,
            'attributes'   => $this->attributes,
            'sender'       => ['sdk' => ['name' => self::SDK_NAME, 'version' => self::SDK_VERSION]],
        ];
    }
}
