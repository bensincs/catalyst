// One status vocabulary for every deployable resource — infrastructure,
// applications, agents, and memory stores. A resource resolves to exactly ONE of
// these, so its state reads the same word everywhere (topology, lists, checklist)
// and we never stack multiple status badges. The underlying signals differ per
// kind (infra_state vs. reconciler health vs. the dependency-wait flag); this
// collapses them into a single, consistent set.

export type StatusTone = "success" | "info" | "warning" | "danger" | "neutral";

export interface ResourceStatus {
  key: "queued" | "waiting" | "deploying" | "live" | "failed" | "drift" | "deprovisioning" | "deleting";
  label: string;
  tone: StatusTone;
  pulse: boolean;
}

const QUEUED: ResourceStatus = { key: "queued", label: "Queued", tone: "neutral", pulse: false };
const WAITING: ResourceStatus = { key: "waiting", label: "Waiting on deps", tone: "warning", pulse: false };
const DEPLOYING: ResourceStatus = { key: "deploying", label: "Deploying", tone: "info", pulse: true };
const LIVE: ResourceStatus = { key: "live", label: "Live", tone: "success", pulse: true };
const FAILED: ResourceStatus = { key: "failed", label: "Failed", tone: "danger", pulse: false };
const DRIFT: ResourceStatus = { key: "drift", label: "Drift", tone: "warning", pulse: false };
const DEPROVISIONING: ResourceStatus = { key: "deprovisioning", label: "Deprovisioning", tone: "info", pulse: true };
/** DELETING is the definition-level counterpart: the catalog entry itself is
 *  being removed once its last provisioned instance is torn down. */
export const DELETING: ResourceStatus = { key: "deleting", label: "Deleting", tone: "warning", pulse: true };

// Reconciler health (applications / agents / memory stores) → unified status.
function fromHealth(health?: string): ResourceStatus {
  switch (health) {
    case "live":
      return LIVE;
    case "blocked":
      return FAILED;
    case "drift":
      return DRIFT;
    case "reconciling":
      return DEPLOYING;
    default:
      // unknown / unreported / disabled / absent — enabled but not yet confirmed.
      return QUEUED;
  }
}

/** Infrastructure (Azure/Bicep) — infra_state is the single source of truth
 *  (its health + waiting flags are derived from it). "" = enabled, not started. */
export function infraStatus(infraState?: string): ResourceStatus {
  switch (infraState) {
    case "ready":
      return LIVE;
    case "failed":
      return FAILED;
    case "provisioning":
      return DEPLOYING;
    case "deprovisioning":
      return DEPROVISIONING;
    default:
      return QUEUED;
  }
}

/** Applications (Helm) — held on dependencies first, else reconciler health. */
export function applicationStatus(a: { health?: string; waiting?: boolean }): ResourceStatus {
  return a.waiting ? WAITING : fromHealth(a.health);
}

/** Agents — reconciler health only. */
export function agentStatus(a: { health?: string }): ResourceStatus {
  return fromHealth(a.health);
}

/** Memory stores — reconciler health only. */
export function storeStatus(s: { health?: string }): ResourceStatus {
  return fromHealth(s.health);
}
