<?php

declare(strict_types=1);

namespace BugBarn;

final class User
{
    private static ?array $current = null;

    public static function setUser(string $id = '', string $email = '', string $username = ''): void
    {
        self::$current = array_filter(compact('id', 'email', 'username'));
    }

    public static function clearUser(): void
    {
        self::$current = null;
    }

    /** @return array<string,string>|null */
    public static function getUser(): ?array
    {
        return self::$current;
    }

    public static function reset(): void
    {
        self::$current = null;
    }
}
