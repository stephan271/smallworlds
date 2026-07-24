// Typed client for the in-cluster Operator Console API (internal/console). The
// console is a separate execution surface from the Bootstrap Launcher: it
// authenticates through Keycloak OIDC and governs every route with a server-side
// Console Role. These types mirror the Go DTOs; the assessment rules stay in the
// backend and are never reproduced here.

export type CapabilityState =
  | 'planned'
  | 'blocked'
  | 'installing'
  | 'healthy'
  | 'degraded'
  | 'failed'
  | 'disabled';

export type FacetKind = 'configuration' | 'delivery' | 'runtime' | 'access' | 'protection';

export type FacetState =
  | 'satisfied'
  | 'pending'
  | 'progressing'
  | 'degraded'
  | 'failed'
  | 'blocked'
  | 'unknown'
  | 'not-applicable';

export type RemediationKind =
  | 'setup-journey'
  | 'git-proposal'
  | 'runtime-action'
  | 'documentation'
  | 'grafana'
  | 'argocd';

export type Permission = 'observe' | 'propose' | 'administer';

export type ConsoleRole = 'observer' | 'operator' | 'owner' | '';

export interface Remediation {
  kind: RemediationKind;
  reference?: string;
}

export interface Facet {
  kind: FacetKind;
  state: FacetState;
  reasonCode: string;
  observedAt?: string;
  stale: boolean;
  remediation?: Remediation;
  /** Resolved contextual URL for the remediation route, set by the server for
   *  external investigation tools (Grafana, Argo CD). Opened in a new tab. */
  remediationUrl?: string;
}

export interface CapabilityAssessment {
  capabilityId: string;
  state: CapabilityState;
  reasonCode: string;
  facets: Facet[];
  observedAt?: string;
}

export interface CapabilitySummary {
  id: string;
  state: CapabilityState;
  reasonCode: string;
}

export interface Overview {
  total: number;
  byState: Partial<Record<CapabilityState, number>>;
  capabilities: CapabilitySummary[];
}

export interface Session {
  authenticated: boolean;
  subject?: string;
  username?: string;
  role?: ConsoleRole;
  permissions: Permission[];
}

export type ProtectionLevel = 'unknown' | 'none' | 'local-only' | 'stale' | 'protected';

export type DataType = 'database' | 'filesystem' | 'object-store' | 'cluster-resources';

export interface ProtectionDataset {
  id: string;
  capability: string;
  dataType: DataType;
  producer: string;
  schedule: string;
  retention: string;
}

export interface DatasetProtection {
  dataset: ProtectionDataset;
  observed: boolean;
  observedAt?: string;
  jobCompletedAt?: string;
  jobFailed: boolean;
  localRecoveryPointAt?: string;
  localRecoveryPointStale: boolean;
  offsiteConfigured: boolean;
  offsiteRecoveryPointAt?: string;
  offsiteRecoveryPointStale: boolean;
  retentionBreached: boolean;
  restoreDrillAt?: string;
  restoreDrillPassed: boolean;
  level: ProtectionLevel;
  disasterProtected: boolean;
}

export interface ProtectionReport {
  datasets: DatasetProtection[];
}

// --- Add-capability journey (internal/addcapability, internal/console) ---

export interface Resources {
  memoryMi: number;
  storageGi: number;
}

/** An addable Community Application: disabled, optional, supported in the
 *  cluster's deployment mode. `disabledDependencies` are the still-disabled
 *  dependencies a proposal would pull in alongside it. */
export interface AddCapabilityOffer {
  id: string;
  displayKey: string;
  resources: Resources;
  exposure: string;
  protection: string;
  stateful: boolean;
  dependencies: string[] | null;
  disabledDependencies: string[] | null;
}

export interface ResourceComparison {
  requiredMemoryMi: number;
  requiredStorageGi: number;
  availableMemoryMi: number;
  availableStorageGi: number;
  fitsMemory: boolean;
  fitsStorage: boolean;
}

export interface AddCapabilityPlan {
  target: string;
  addedCapabilities: string[];
  presentDependencies: string[] | null;
  resources: ResourceComparison;
  exposure: string[] | null;
  protection: string[] | null;
  persistentData: string[] | null;
  gitDiff: string;
}

export interface PlanResponse {
  planId: string;
  digest: string;
  summary: string;
  plan: AddCapabilityPlan;
}

export interface ProposeResponse {
  runId: string;
  provider: string;
  branch?: string;
  commit: string;
  url?: string;
  mergeObserved: boolean;
  mergeInstructionKey: string;
}

// --- Device access administration (internal/operatordevice, internal/console) ---

export type EnrollmentStepKind =
  | 'acquire-tailscale-client'
  | 'join-private-network'
  | 'configure-private-dns'
  | 'install-cluster-ca'
  | 'verify-gateway-access';

export interface EnrollmentStep {
  kind: EnrollmentStepKind;
  elevationRequired: boolean;
}

export interface EnrollmentGuidance {
  mode: string;
  clusterCaTrustRequired: boolean;
  gatewayHostname: string;
  operatorHostnames: string[];
  steps: EnrollmentStep[];
}

export interface OperatorDevice {
  stableId: string;
  hostname: string;
  label?: string;
  ownerAccess: boolean;
  self: boolean;
  online: boolean;
  lastSeen?: string;
}

/** One entry in the shared Activity Record projected for the Owner surface. */
export interface AdminActivity {
  id: string;
  planId: string;
  phase: string;
  checkpoint?: string;
  evidenceSummary?: string;
  startedAt: string;
}

export interface AccessSummary {
  totalDevices: number;
  ownerDevices: number;
}

export interface AdminAccess {
  devices: OperatorDevice[];
  summary: AccessSummary;
  activity: AdminActivity[];
  guidance?: EnrollmentGuidance;
}

export interface InvitationResponse {
  invitationId: string;
  label: string;
  issuedBy: string;
  keyFingerprint: string;
  expiresAt: string;
  singleUse: boolean;
  /** The single-use join key, shown exactly once. Never persisted. */
  joinKey: string;
  guidance?: EnrollmentGuidance;
}

export type LockoutReason = 'last-owner-device' | 'self-revocation' | '';

export interface RevocationAssessment {
  affectedStableId: string;
  target: OperatorDevice;
  remainingOwnerDevices: number;
  alternativeOwnerAccess: boolean;
  totalDevices: number;
  lockoutRisk: boolean;
  lockoutReason?: LockoutReason;
}

export interface RevocationPlanResponse {
  planId: string;
  digest: string;
  assessment: RevocationAssessment;
}

export interface RevocationExecuteResponse {
  runId: string;
  phase: string;
  affectedStableId: string;
  accessVerified: boolean;
}

export class ConsoleApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code?: string
  ) {
    super(`console api error: ${status}`);
  }
}

async function getJSON<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: { Accept: 'application/json' } });
  if (!response.ok) {
    throw new ConsoleApiError(response.status);
  }
  return (await response.json()) as T;
}

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    headers: body
      ? { Accept: 'application/json', 'Content-Type': 'application/json' }
      : { Accept: 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  if (!response.ok) {
    let code: string | undefined;
    try {
      code = ((await response.json()) as { code?: string }).code;
    } catch {
      code = undefined;
    }
    throw new ConsoleApiError(response.status, code);
  }
  return (await response.json()) as T;
}

export const consoleApi = {
  loginPath: '/api/v1/auth/login',
  session: () => getJSON<Session>('/api/v1/session'),
  overview: () => getJSON<Overview>('/api/v1/overview'),
  capability: (id: string) =>
    getJSON<CapabilityAssessment>(`/api/v1/capabilities/${encodeURIComponent(id)}`),
  protection: () => getJSON<ProtectionReport>('/api/v1/protection'),
  additionOffers: () => getJSON<{ offers: AddCapabilityOffer[] }>('/api/v1/additions/offers'),
  planAddition: (capabilityId: string) =>
    postJSON<PlanResponse>('/api/v1/additions/plan', { capabilityId }),
  approveAddition: (planId: string) =>
    postJSON<{ planId: string; approvedBy: string }>(
      `/api/v1/additions/${encodeURIComponent(planId)}/approve`
    ),
  proposeAddition: (planId: string) =>
    postJSON<ProposeResponse>(`/api/v1/additions/${encodeURIComponent(planId)}/propose`),
  administrationAccess: () => getJSON<AdminAccess>('/api/v1/administration/access'),
  createInvitation: (label: string) =>
    postJSON<InvitationResponse>('/api/v1/administration/invitations', { label }),
  planRevocation: (stableId: string) =>
    postJSON<RevocationPlanResponse>('/api/v1/administration/revocations/plan', { stableId }),
  approveRevocation: (planId: string) =>
    postJSON<{ planId: string; approvedBy: string }>(
      `/api/v1/administration/revocations/${encodeURIComponent(planId)}/approve`
    ),
  executeRevocation: (planId: string) =>
    postJSON<RevocationExecuteResponse>(
      `/api/v1/administration/revocations/${encodeURIComponent(planId)}/execute`
    ),
  logout: () => fetch('/api/v1/auth/logout', { method: 'POST' })
};

export function hasPermission(session: Session | null, permission: Permission): boolean {
  return !!session?.permissions?.includes(permission);
}
