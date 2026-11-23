import { recheckCollection, submitClusterMaintenance, submitOnboarding } from "./views/clusters/cluster-onboarding.ts";
import { submitNamespaceOwnership } from "./views/clusters/namespace-ownership.ts";
import { submitIdentityAdmin } from "./views/permissions/identity-admin.ts";
import { submitSessionPolicy, updateSessionPolicy } from "./views/permissions/session-policy.ts";
import { submitPlatform } from "./views/permissions/platform-admin.ts";
import { submitRole } from "./views/permissions/roles.ts";
import { submitResourceCatalog } from "./views/resources/resource-management.ts";

interface PortalFormActions {
  refresh: () => void;
  completePasswordChange: () => void;
  sessionExpired: () => void;
  login: (event: Event) => void;
}

/** 将控制台表单提交转交给所属功能模块。 */
export function bindPortalForms(root: HTMLElement, actions: PortalFormActions): void {
  root.addEventListener("input", (event) => updateSessionPolicy(event.target));
  root.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    const submitter = event instanceof SubmitEvent ? event.submitter : null;
    const action = submitter?.getAttribute("value") ?? "";
    if (form.hasAttribute("data-role-form")) {
      event.preventDefault();
      void submitRole(form, action === "delete");
      return;
    }
    if (form.dataset.resourceCatalog) {
      event.preventDefault();
      void submitResourceCatalog(form, action === "delete", actions.refresh);
      return;
    }
    if (form.dataset.clusterMaintenance) {
      event.preventDefault();
      void submitClusterMaintenance(form, actions.refresh);
      return;
    }
    if (form.dataset.collectionCheck) {
      event.preventDefault();
      void recheckCollection(form, actions.refresh);
      return;
    }
    if (form.dataset.onboardingMode) {
      event.preventDefault();
      void submitOnboarding(form, action === "test", actions.refresh);
      return;
    }
    if (form.dataset.namespaceOwnership) {
      event.preventDefault();
      void submitNamespaceOwnership(form);
      return;
    }
    if (form.dataset.platform) {
      event.preventDefault();
      if (action) void submitPlatform(form, action, actions.refresh);
      return;
    }
    if (form.hasAttribute("data-session-policy")) {
      event.preventDefault();
      void submitSessionPolicy(form, actions.sessionExpired);
      return;
    }
    if (form.dataset.identityResource) {
      event.preventDefault();
      void submitIdentityAdmin(form, actions.completePasswordChange, action === "test");
      return;
    }
    actions.login(event);
  });
}
