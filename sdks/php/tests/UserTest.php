<?php

declare(strict_types=1);

namespace BugBarn\Tests;

use BugBarn\Breadcrumbs;
use BugBarn\Client;
use BugBarn\Envelope;
use BugBarn\User;
use PHPUnit\Framework\TestCase;

final class UserTest extends TestCase
{
    protected function tearDown(): void
    {
        Client::shutdown();
        User::reset();
        Breadcrumbs::reset();
    }

    public function testDefaultIsNull(): void
    {
        self::assertNull(User::getUser());
    }

    public function testSetUserStoresValues(): void
    {
        User::setUser('42', 'alice@example.com', 'alice');

        $user = User::getUser();

        self::assertNotNull($user);
        self::assertSame('42', $user['id']);
        self::assertSame('alice@example.com', $user['email']);
        self::assertSame('alice', $user['username']);
    }

    public function testClearUserRemovesUser(): void
    {
        User::setUser('42', 'alice@example.com', 'alice');
        User::clearUser();

        self::assertNull(User::getUser());
    }

    public function testUserAppearsInPayload(): void
    {
        $env = new Envelope(
            timestamp:        '2026-01-01T00:00:00.000Z',
            severityText:     'ERROR',
            body:             'test',
            exceptionType:    'RuntimeException',
            exceptionMessage: 'test',
            user:             ['id' => '99', 'email' => 'bob@example.com'],
        );

        $payload = $env->toPayload();

        self::assertArrayHasKey('user', $payload);
        self::assertSame('99', $payload['user']['id']);
        self::assertSame('bob@example.com', $payload['user']['email']);
    }

    public function testUserAppearsInCapturedEvent(): void
    {
        Client::init('test-key', 'http://localhost:19999/api/v1/events');
        Client::setUser('7', 'carol@example.com', 'carol');

        // We can only verify captureException returns true (transport is live).
        // The payload shape is validated via the Envelope unit test above.
        self::assertTrue(Client::captureException(new \RuntimeException('user context test')));
    }

    public function testClearUserViaClientProxy(): void
    {
        Client::setUser('7', 'carol@example.com', 'carol');
        Client::clearUser();

        self::assertNull(User::getUser());
    }
}
