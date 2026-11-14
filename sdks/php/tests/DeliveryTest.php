<?php

declare(strict_types=1);

namespace BugBarn\Tests;

use BugBarn\Breadcrumbs;
use BugBarn\Client;
use BugBarn\Envelope;
use BugBarn\Transport;
use BugBarn\User;
use PHPUnit\Framework\TestCase;

/**
 * Sends real requests to a PHP built-in server on the loopback interface, so the
 * transport is tested end to end without reaching any network.
 */
final class DeliveryTest extends TestCase
{
    /** @var resource|null */
    private $server = null;
    private string $log = '';
    private int $port = 0;

    protected function setUp(): void
    {
        $this->log = tempnam(sys_get_temp_dir(), 'bugbarn-php-');
        $probe = stream_socket_server('tcp://127.0.0.1:0');
        $this->port = (int) substr((string) strrchr((string) stream_socket_get_name($probe, false), ':'), 1);
        fclose($probe);

        $this->server = proc_open(
            [PHP_BINARY, '-S', '127.0.0.1:' . $this->port, __DIR__ . '/fixtures/router.php'],
            [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']],
            $pipes,
            null,
            ['BUGBARN_TEST_LOG' => $this->log],
        );
        $this->waitForServer();
    }

    protected function tearDown(): void
    {
        Client::shutdown();
        User::reset();
        Breadcrumbs::reset();
        if (is_resource($this->server)) {
            proc_terminate($this->server);
            proc_close($this->server);
        }
        @unlink($this->log);
    }

    private function waitForServer(): void
    {
        $deadline = microtime(true) + 10.0;
        while (microtime(true) < $deadline) {
            $conn = @fsockopen('127.0.0.1', $this->port, $errno, $errstr, 0.2);
            if ($conn !== false) {
                fclose($conn);
                return;
            }
            usleep(20_000);
        }
        self::fail('the local test server did not start');
    }

    private function url(): string
    {
        return 'http://127.0.0.1:' . $this->port . '/api/v1/events';
    }

    /** @return array<int,array<string,mixed>> */
    private function requests(): array
    {
        $lines = file($this->log, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [];
        return array_map(static fn (string $l): array => json_decode($l, true), $lines);
    }

    public function testFlushDeliversExceptionWithStacktraceAndContext(): void
    {
        Client::init('key-1', $this->url(), projectSlug: 'web', release: '3.2.1');
        Client::setUser('42', 'a@example.com', 'alice');
        Client::addBreadcrumb('nav', 'opened /home', 'info', ['k' => 'v']);

        self::assertTrue(Client::captureException(new \LogicException('bad state'), ['plan' => 'pro']));
        Client::flush();

        $requests = $this->requests();
        self::assertCount(1, $requests);
        self::assertSame('/api/v1/events', $requests[0]['path']);
        self::assertSame('key-1', $requests[0]['headers']['X-BugBarn-Api-Key']);
        self::assertSame('web', $requests[0]['headers']['X-BugBarn-Project']);

        $body = $requests[0]['body'];
        self::assertSame('bad state', $body['body']);
        self::assertSame('LogicException', $body['exception']['type']);
        self::assertSame('pro', $body['attributes']['plan']);
        self::assertSame('3.2.1', $body['attributes']['release']);
        self::assertSame('opened /home', $body['breadcrumbs'][0]['message']);
        self::assertNotEmpty($body['exception']['stacktrace']);
        $functions = array_column($body['exception']['stacktrace'], 'function');
        self::assertContains('testFlushDeliversExceptionWithStacktraceAndContext', $functions);
    }

    public function testFlushDeliversMessagesInOrderAndEmptiesTheQueue(): void
    {
        Client::init('key-2', $this->url());

        Client::captureMessage('first');
        Client::captureMessage('second');
        Client::flush();
        Client::flush();

        $bodies = array_map(static fn (array $r): string => $r['body']['body'], $this->requests());
        self::assertSame(['first', 'second'], $bodies);
    }

    public function testRequestOmitsTheProjectHeaderWithoutASlug(): void
    {
        Client::init('key-3', $this->url());
        Client::captureMessage('no project');
        Client::flush();

        $headers = $this->requests()[0]['headers'];
        self::assertArrayNotHasKey('X-BugBarn-Project', $headers);
    }

    public function testBaseUrlGetsTheEventsPathAppended(): void
    {
        Client::init('key-4', 'http://127.0.0.1:' . $this->port . '/');
        Client::captureMessage('base url');
        Client::flush();

        self::assertSame('/api/v1/events', $this->requests()[0]['path']);
    }

    public function testInstalledExceptionHandlerCapturesAndFlushes(): void
    {
        Client::init('key-5', $this->url(), installHandlers: true);

        // set_exception_handler returns the handler it replaces, which is ours.
        $handler = set_exception_handler(null);
        try {
            self::assertIsCallable($handler);
            $handler(new \DomainException('uncaught'));
        } finally {
            restore_exception_handler(); // drops the null handler set above
            restore_exception_handler(); // drops the handler Client::init installed
        }

        $requests = $this->requests();
        self::assertCount(1, $requests);
        self::assertSame('DomainException', $requests[0]['body']['exception']['type']);
    }

    public function testUnreachableEndpointDoesNotThrow(): void
    {
        $transport = new Transport('k', 'http://127.0.0.1:1/api/v1/events', timeoutSeconds: 0.2);
        $transport->enqueue(new Envelope('t', 'ERROR', 'b', 'Error', 'b'));

        $transport->flush();

        $this->addToAssertionCount(1);
    }
}
