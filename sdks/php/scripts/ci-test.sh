#!/bin/sh
# Runs the PHP SDK tests with line coverage. Meant to run inside the pinned
# composer container with the SDK mounted read-only at /src:
#
#   docker run --rm -v "$(pwd):/src:ro" composer:2.8 sh /src/scripts/ci-test.sh
#
# The SDK is copied to /app so composer writes vendor/ to the container's own
# filesystem. PCOV is built from source because the composer image ships no
# coverage driver; it adds about 25 seconds. The phpunit text report goes to
# stdout, where scripts/check-sdk-coverage.sh reads its "Lines:" total.
set -eu

cp -R /src/. /app
cd /app

# apk and pecl print a lot; keep their output out of the report unless the
# build fails.
log=$(mktemp)
if ! { apk add --no-cache $PHPIZE_DEPS &&
	pecl install pcov &&
	docker-php-ext-enable pcov; } >"$log" 2>&1; then
	cat "$log"
	echo "FAIL: could not install the pcov coverage driver"
	exit 1
fi

composer install --no-interaction --prefer-dist --no-progress
./vendor/bin/phpunit --coverage-text
