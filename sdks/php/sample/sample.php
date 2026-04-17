<?php

declare(strict_types=1);

require __DIR__ . '/../vendor/autoload.php';

use BugBarn\Client;

$endpoint = getenv('BUGBARN_ENDPOINT') ?: 'http://localhost:8080/api/v1/events';
$apiKey   = getenv('BUGBARN_API_KEY')  ?: '';
$project  = getenv('BUGBARN_PROJECT')  ?: '';

if ($apiKey === '') {
    echo "Set BUGBARN_API_KEY and BUGBARN_ENDPOINT before running.\n";
    exit(1);
}

Client::init(
    apiKey:          $apiKey,
    endpoint:        $endpoint,
    installHandlers: true,
    projectSlug:     $project,
);

// Manual capture.
try {
    throw new \RuntimeException('Something went wrong in PHP');
} catch (\Throwable $e) {
    $sent = Client::captureException($e, ['environment' => 'local', 'service' => 'php-sample']);
    echo $sent ? "Queued exception for delivery.\n" : "Failed to queue exception.\n";
}

// Uncaught — will be captured by the installed exception handler, flushed,
// and then PHP's default behaviour (fatal output + exit) follows.
throw new \LogicException('Uncaught PHP exception — captured by BugBarn shutdown handler');
