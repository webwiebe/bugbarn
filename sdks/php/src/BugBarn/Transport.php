<?php

declare(strict_types=1);

namespace BugBarn;

final class Transport
{
    /** @var Envelope[] */
    private array $queue = [];

    public function __construct(
        private readonly string $apiKey,
        private readonly string $endpoint,
        private readonly int    $maxQueueSize   = 256,
        private readonly float  $timeoutSeconds = 2.0,
        private readonly string $projectSlug    = '',
    ) {}

    public function enqueue(Envelope $envelope): bool
    {
        if (count($this->queue) >= $this->maxQueueSize) {
            return false;
        }
        $this->queue[] = $envelope;
        return true;
    }

    public function flush(): void
    {
        while ($this->queue !== []) {
            $this->send(array_shift($this->queue));
        }
    }

    private function send(Envelope $envelope): void
    {
        $body = json_encode(
            $envelope->toPayload(),
            JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES,
        );
        if ($body === false) {
            return;
        }

        $ch = curl_init($this->endpoint);
        if ($ch === false) {
            return;
        }

        $headers = [
            'Content-Type: application/json',
            'X-BugBarn-Api-Key: ' . $this->apiKey,
        ];
        if ($this->projectSlug !== '') {
            $headers[] = 'X-BugBarn-Project: ' . $this->projectSlug;
        }

        curl_setopt_array($ch, [
            CURLOPT_POST           => true,
            CURLOPT_POSTFIELDS     => $body,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_TIMEOUT_MS     => (int) ($this->timeoutSeconds * 1_000),
            CURLOPT_HTTPHEADER     => $headers,
        ]);

        curl_exec($ch);
        curl_close($ch);
    }
}
