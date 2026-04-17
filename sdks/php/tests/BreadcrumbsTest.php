<?php

declare(strict_types=1);

namespace BugBarn\Tests;

use BugBarn\Breadcrumbs;
use BugBarn\Client;
use BugBarn\Envelope;
use BugBarn\User;
use PHPUnit\Framework\TestCase;

final class BreadcrumbsTest extends TestCase
{
    protected function tearDown(): void
    {
        Client::shutdown();
        User::reset();
        Breadcrumbs::reset();
    }

    public function testAddBreadcrumb(): void
    {
        Breadcrumbs::add('navigation', 'user navigated to /home');

        $crumbs = Breadcrumbs::get();

        self::assertCount(1, $crumbs);
        self::assertSame('navigation', $crumbs[0]['category']);
        self::assertSame('user navigated to /home', $crumbs[0]['message']);
        self::assertArrayHasKey('timestamp', $crumbs[0]);
    }

    public function testCapAtMax(): void
    {
        for ($i = 0; $i <= 100; $i++) {
            Breadcrumbs::add('test', "event $i");
        }

        self::assertCount(100, Breadcrumbs::get());
    }

    public function testClearBreadcrumbs(): void
    {
        Breadcrumbs::add('test', 'something happened');
        Breadcrumbs::clear();

        self::assertCount(0, Breadcrumbs::get());
    }

    public function testBreadcrumbsAppearInPayload(): void
    {
        $crumbs = [
            ['timestamp' => '2026-01-01T00:00:00.000Z', 'category' => 'http', 'message' => 'GET /api/users', 'level' => 'info'],
            ['timestamp' => '2026-01-01T00:00:01.000Z', 'category' => 'db', 'message' => 'SELECT * FROM users'],
        ];

        $env = new Envelope(
            timestamp:        '2026-01-01T00:00:02.000Z',
            severityText:     'ERROR',
            body:             'test',
            exceptionType:    'RuntimeException',
            exceptionMessage: 'test',
            breadcrumbs:      $crumbs,
        );

        $payload = $env->toPayload();

        self::assertArrayHasKey('breadcrumbs', $payload);
        self::assertCount(2, $payload['breadcrumbs']);
        self::assertSame('http', $payload['breadcrumbs'][0]['category']);
        self::assertSame('GET /api/users', $payload['breadcrumbs'][0]['message']);
        self::assertSame('db', $payload['breadcrumbs'][1]['category']);
    }

    public function testBreadcrumbsAppearInCapturedEvent(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');
        Client::addBreadcrumb('http', 'GET /api/data', 'info');

        self::assertTrue(Client::captureException(new \RuntimeException('breadcrumb test')));
    }

    public function testClearBreadcrumbsViaClientProxy(): void
    {
        Client::addBreadcrumb('test', 'something');
        Client::clearBreadcrumbs();

        self::assertCount(0, Breadcrumbs::get());
    }

    public function testBreadcrumbWithLevelAndData(): void
    {
        Breadcrumbs::add('query', 'SELECT 1', 'debug', ['duration_ms' => 5]);

        $crumbs = Breadcrumbs::get();

        self::assertSame('debug', $crumbs[0]['level']);
        self::assertSame(['duration_ms' => 5], $crumbs[0]['data']);
    }
}
