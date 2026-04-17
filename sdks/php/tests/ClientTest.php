<?php

declare(strict_types=1);

namespace BugBarn\Tests;

use BugBarn\Client;
use BugBarn\Envelope;
use PHPUnit\Framework\TestCase;

final class ClientTest extends TestCase
{
    protected function tearDown(): void
    {
        Client::shutdown();
    }

    public function testCaptureExceptionBeforeInitReturnsFalse(): void
    {
        self::assertFalse(
            Client::captureException(new \RuntimeException('before init'))
        );
    }

    public function testCaptureExceptionAfterInitReturnsTrue(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');

        self::assertTrue(
            Client::captureException(new \RuntimeException('test error'))
        );
    }

    public function testCaptureMessageAfterInitReturnsTrue(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');

        self::assertTrue(Client::captureMessage('something happened'));
    }

    public function testCaptureMessageBeforeInitReturnsFalse(): void
    {
        self::assertFalse(Client::captureMessage('not initialised'));
    }

    public function testFlushIsNoopWhenNotInitialised(): void
    {
        Client::flush(); // must not throw
        $this->addToAssertionCount(1);
    }

    public function testShutdownResetsState(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');
        Client::captureException(new \RuntimeException('buffered'));
        Client::shutdown();

        // After shutdown, capture returns false (transport detached).
        self::assertFalse(
            Client::captureException(new \RuntimeException('after shutdown'))
        );
    }

    public function testEnvelopePayloadShape(): void
    {
        $env = new Envelope(
            timestamp:        '2026-01-01T00:00:00.000Z',
            severityText:     'ERROR',
            body:             'test message',
            exceptionType:    'RuntimeException',
            exceptionMessage: 'test message',
            stacktrace:       null,
            attributes:       ['service' => 'my-app'],
        );

        $payload = $env->toPayload();

        self::assertSame('ERROR', $payload['severityText']);
        self::assertSame('RuntimeException', $payload['exception']['type']);
        self::assertSame('test message', $payload['exception']['message']);
        self::assertSame('my-app', $payload['attributes']['service']);
        self::assertArrayHasKey('sender', $payload);
        self::assertArrayHasKey('sdk', $payload['sender']);
        self::assertSame('bugbarn.php', $payload['sender']['sdk']['name']);
        self::assertArrayNotHasKey('stacktrace', $payload['exception']);
    }

    public function testEnvelopeIncludesStacktraceWhenProvided(): void
    {
        $frames = [
            ['function' => 'myFunc', 'file' => '/app/index.php', 'line' => 10, 'module' => 'index.php'],
        ];
        $env = new Envelope(
            timestamp:        '2026-01-01T00:00:00.000Z',
            severityText:     'ERROR',
            body:             'test',
            exceptionType:    'Error',
            exceptionMessage: 'test',
            stacktrace:       $frames,
        );

        $payload = $env->toPayload();

        self::assertSame($frames, $payload['exception']['stacktrace']);
    }

    public function testQueueCapExcludesOverflow(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');

        // The queue cap is 256 by default; enqueue 256 events and verify they
        // all succeed, then the 257th should fail without throwing.
        for ($i = 0; $i < 256; $i++) {
            Client::captureMessage("event $i");
        }
        // 257th must not throw and must indicate it was dropped.
        // captureMessage returns false when the queue is full.
        self::assertFalse(Client::captureMessage('overflow'));
    }
}
