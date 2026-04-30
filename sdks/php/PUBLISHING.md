# Publishing to Packagist

Packagist syncs automatically from GitHub tags — no CI job is needed. Follow these steps once to register the package.

## One-time submission

1. Go to [packagist.org/packages/submit](https://packagist.org/packages/submit) and log in.
2. Enter the GitHub repository URL (e.g. `https://github.com/webwiebe/bugbarn`) and click **Check**.
3. Confirm the package name (`bugbarn/bugbarn-php`) and click **Submit**.

## GitHub webhook (auto-update on push)

After submission, Packagist shows a webhook URL. Add it to the GitHub repo so Packagist picks up new tags immediately:

1. In the GitHub repo go to **Settings → Webhooks → Add webhook**.
2. Set **Payload URL** to the URL shown on your Packagist package page (format: `https://packagist.org/api/github?username=<your-packagist-username>`).
3. Set **Content type** to `application/json`.
4. Under **Which events**, choose **Just the push event**.
5. Click **Add webhook**.

From this point on, every `git push --tags` triggers Packagist to pull the latest release automatically.

## Required secrets

None — Packagist needs no CI secrets. Tags pushed by the existing `binary-release.yml` workflow will trigger the webhook.
