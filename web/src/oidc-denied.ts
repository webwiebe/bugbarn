// The text of the card shown when the OIDC callback refuses a sign-in.

export function oidcDeniedMessage(code: string, identity: string): string {
  const who = identity ? ` as ${identity}` : "";
  if (code === "wrong_organization") {
    return `You're signed in to IAMBarn${who} in a different organization than this BugBarn belongs to. Switch to your home organization in IAMBarn, then sign in again.`;
  }
  return `You're signed in to IAMBarn${who}, but that account doesn't have access to BugBarn.`;
}
