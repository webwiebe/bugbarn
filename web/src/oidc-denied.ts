// The text and links of the card shown when the OIDC callback refuses a sign-in.

type DeniedLink = { label: string; href: string };
type DeniedRuntime = { loginURL?: string; switchAccountURL?: string; endSessionURL?: string };

const wrongOrganization = "wrong_organization";

export function oidcDeniedMessage(code: string, identity: string): string {
  const who = identity ? ` as ${identity}` : "";
  if (code === wrongOrganization) {
    return `You're signed in to IAMBarn${who} in a different organization than this BugBarn belongs to. Switch to your home organization in IAMBarn, then sign in again.`;
  }
  return `You're signed in to IAMBarn${who}, but that account doesn't have access to BugBarn.`;
}

function switchOrganizationLinks(iambarnURL: string, loginURL: string | undefined): DeniedLink[] {
  const links: DeniedLink[] = [];
  if (iambarnURL) links.push({ label: "Open IAMBarn", href: iambarnURL });
  if (loginURL) links.push({ label: "Sign in again", href: loginURL });
  return links;
}

function signOutHref(endSessionURL: string, origin: string): string {
  const sep = endSessionURL.includes("?") ? "&" : "?";
  return `${endSessionURL}${sep}post_logout_redirect_uri=${encodeURIComponent(`${origin}/`)}`;
}

export function oidcDeniedLinks(
  code: string,
  runtime: DeniedRuntime | undefined,
  iambarnURL: string,
  origin: string,
): DeniedLink[] {
  const links = code === wrongOrganization ? switchOrganizationLinks(iambarnURL, runtime?.loginURL) : [];
  const account = runtime?.switchAccountURL ?? runtime?.loginURL;
  if (account) links.push({ label: "Switch account", href: account });
  if (runtime?.endSessionURL) {
    links.push({ label: "Sign out of IAMBarn", href: signOutHref(runtime.endSessionURL, origin) });
  }
  return links;
}
