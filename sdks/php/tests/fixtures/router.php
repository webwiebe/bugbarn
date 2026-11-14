<?php

declare(strict_types=1);

// Router for the PHP built-in server used by DeliveryTest. It appends one JSON
// line per request to the file named by the BUGBARN_TEST_LOG environment
// variable and answers 202.
$line = json_encode([
    'path'    => $_SERVER['REQUEST_URI'],
    'headers' => getallheaders(),
    'body'    => json_decode((string) file_get_contents('php://input'), true),
]);
file_put_contents((string) getenv('BUGBARN_TEST_LOG'), $line . "\n", FILE_APPEND | LOCK_EX);
http_response_code(202);
