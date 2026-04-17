<?php

declare(strict_types=1);

namespace BugBarn;

final class Breadcrumbs
{
    private const MAX = 100;

    /** @var array<array<string,mixed>> */
    private static array $buffer = [];

    /** @param array<string,mixed>|null $data */
    public static function add(string $category, string $message, string $level = '', ?array $data = null): void
    {
        $crumb = array_filter([
            'timestamp' => (new \DateTimeImmutable())->format(\DateTimeInterface::RFC3339_EXTENDED),
            'category'  => $category,
            'message'   => $message,
            'level'     => $level,
            'data'      => $data,
        ]);
        self::$buffer[] = $crumb;
        if (count(self::$buffer) > self::MAX) {
            array_shift(self::$buffer);
        }
    }

    /** @return array<array<string,mixed>> */
    public static function get(): array
    {
        return self::$buffer;
    }

    public static function clear(): void
    {
        self::$buffer = [];
    }

    public static function reset(): void
    {
        self::$buffer = [];
    }
}
