// Account view: hosts IAMBarn's <iambarn-profile> editor inside BugBarn's shell.
// Reached from the hosted user menu's "Manage account" link (account-href="#/account")
// or directly via the #/account hash. The profile element saves to IAMBarn directly;
// BugBarn owns only the surrounding page.
import { elements, setActiveView } from "./core.js";
import { mountIAMBarnProfile } from "./iambarn.js";

export function renderAccountView(): void {
  setActiveView("overview");
  elements.detailTitle.textContent = "Account";
  elements.detailBody.innerHTML = "";
  elements.overviewView.innerHTML = `
    <div class="view-head">
      <h2>Account</h2>
    </div>
    <div id="iambarn-profile-host" class="account-profile-host"></div>`;
  const host = elements.overviewView.querySelector<HTMLElement>("#iambarn-profile-host");
  if (host) void mountIAMBarnProfile(host);
}
