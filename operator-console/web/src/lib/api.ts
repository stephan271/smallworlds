import type { components } from './generated/api';

export type ClusterProfile = components['schemas']['ClusterProfile'];
export type ProfileInput = components['schemas']['ProfileInput'];
export type SetupJourney = components['schemas']['SetupJourney'];
/** Every non-secret answer the operator has already given. Saved on the Launcher
 *  Host so closing the console never costs them a retyped domain or host name. */
export type SetupSettings = components['schemas']['SetupSettings'];
export type ChangePlan = components['schemas']['ChangePlan'];
export type WorkflowRun = components['schemas']['WorkflowRun'];
export type VaultStatus = components['schemas']['VaultStatus'];
export type CredentialMetadata = components['schemas']['CredentialMetadata'];
export type RecoveryBundlePreview = components['schemas']['RecoveryBundlePreview'];
export type RecoveryBundleImportResult = components['schemas']['RecoveryBundleImportResult'];
export type CapabilityMode = 'minimal' | 'collaboration' | 'full' | 'custom';
export type CapabilityEntry = { id: string; displayKey: string; category: 'platform-service' | 'community-application'; required: boolean; dependencies: string[]; resources: { memoryMi: number; storageGi: number }; exposure: string; protection: string };
export type CapabilityCatalog = { version: number; capabilities: CapabilityEntry[] };
export type CapabilityPlanResult = { plan: ChangePlan; overlay: { diff: string; assessment: { communityIds: string[]; resources: { memoryMi: number; storageGi: number }; exposure: string[]; protection: string[] } } };
export type GitHubTokenStatus = components['schemas']['GitHubTokenStatus'];
export type GenericGitCredentialStatus = components['schemas']['GenericGitCredentialStatus'];
export type GenericGitProposal = components['schemas']['GenericGitProposal'];
export type BootstrapAssetRequirements = components['schemas']['BootstrapAssetRequirements'];
export type NodeCapabilities = components['schemas']['NodeCapabilities'];
export type NodeTarget = components['schemas']['NodeTarget'];
export type NodeProbeResult = components['schemas']['NodeProbeResult'];
export type NodeTrust = components['schemas']['NodeTrust'];
export type OverlayIdentity = components['schemas']['OverlayIdentity'];
export type NodeInspectionResult = components['schemas']['NodeInspectionResult'];
export type LocalBootstrapPlanResult = components['schemas']['LocalBootstrapPlanResult'];
export type ClusterCAReferenceView = components['schemas']['ClusterCAReferenceView'];
export type ClusterCADeviceTrust = components['schemas']['ClusterCADeviceTrust'];
export type PrivateNetworkReferenceView = components['schemas']['PrivateNetworkReferenceView'];
export type TailscaleClientOffer = components['schemas']['TailscaleClientOffer'];
export type EnrollmentReferenceView = components['schemas']['EnrollmentReferenceView'];
export type HandoffReport = components['schemas']['HandoffReport'];
export type HandoffClosure = components['schemas']['HandoffClosure'];
export type FirstOwnerState = components['schemas']['FirstOwnerState'];
export type HandoffAssessment = components['schemas']['HandoffAssessment'];
/** What the installed cluster is doing right now, in the cluster's own words. */
export type ClusterDetail = components['schemas']['ClusterDetail'];
export type OffsiteProtection = components['schemas']['OffsiteProtection'];
export type OffsitePlan = components['schemas']['OffsitePlan'];
export type OffsiteProposal = components['schemas']['OffsiteProposal'];
export type OffsiteDestinationInput = { profileId: string; endpoint: string; region: string; bucket: string; accessKeyId: string; secretAccessKey: string };
export type HetznerProject = components['schemas']['HetznerProject'];
export type HetznerTokenAssessment = components['schemas']['HetznerTokenAssessment'];
export type HetznerPresets = components['schemas']['HetznerPresets'];
export type HetznerPreset = components['schemas']['HetznerPreset'];
export type HetznerChangePlan = components['schemas']['HetznerChangePlan'];
export type HetznerToolchain = components['schemas']['HetznerToolchain'];
export type HetznerWorkspace = components['schemas']['HetznerWorkspace'];
export type TemporaryAccess = components['schemas']['TemporaryAccess'];
export type HetznerPresetTier = 'small' | 'recommended' | 'high' | 'advanced';
export type HetznerPlanResult = { plan: ChangePlan; changePlan: HetznerChangePlan; approvable: boolean };
export type DecommissionItem = { kind: string; providerId: string; name: string; state: string; ownership: 'profile-owned' | 'shared' | 'retained' | 'unknown'; action: 'remove' | 'retain'; reason: string; monthlyEur?: number; detail?: string };
export type PreserveDataDecommissionPlan = { profileId: string; deploymentMode: string; inspectionDigest: string; items: DecommissionItem[]; retainedData: string[]; continuingCosts: Array<{ providerId: string; kind: string; monthlyEur: number }>; expectedDowntime: string; recoveryPath: string; blockers?: string[]; digest: string };
export type PreserveDataDecommissionResult = { plan: ChangePlan; decommission: PreserveDataDecommissionPlan; approvable: boolean };
export type FullDecommissionItem = DecommissionItem & { stage?: 'compute' | 'storage' | 'networking' | 'dns'; consequence: string };
export type FullDecommissionPlan = { profileId: string; deploymentMode: string; inspectionDigest: string; protection: { backupFreshness: string; offsiteRecoveryPoints: string[]; recoveryBundleStatus: string; sufficient: boolean; warnings?: string[] }; items: FullDecommissionItem[]; irreversibleConsequences: string[]; requiresOwnerOverride: boolean; typedConfirmation: string; digest: string };
export type FullDecommissionResult = { plan: ChangePlan; decommission: FullDecommissionPlan; requiresTypedConfirmation: true };

let csrfToken = '';

async function decode<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const failure = (await response.json().catch(() => ({ code: 'request_failed' }))) as { code: string };
    throw new Error(failure.code);
  }
  return (await response.json()) as T;
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  if (init.body) headers.set('Content-Type', 'application/json');
  if (init.method && init.method !== 'GET') headers.set('X-CSRF-Token', csrfToken);
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  return decode<T>(response);
}

async function requestVoid(path: string, init: RequestInit): Promise<void> {
  const headers = new Headers(init.headers);
  headers.set('X-CSRF-Token', csrfToken);
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  if (!response.ok) await decode<never>(response);
}

async function requestBinary(path: string, init: RequestInit): Promise<Blob> {
  const headers = new Headers(init.headers);
  headers.set('Content-Type', 'application/json');
  headers.set('X-CSRF-Token', csrfToken);
  const response = await fetch(path, { ...init, headers, credentials: 'same-origin' });
  if (!response.ok) await decode<never>(response);
  return response.blob();
}

export async function initializeSession(): Promise<void> {
  const current = await fetch('/api/v1/session', { credentials: 'same-origin' });
  if (current.ok) {
    const session = (await current.json()) as components['schemas']['Session'];
    csrfToken = session.csrfToken;
    scrubLaunchToken();
    return;
  }
  const url = new URL(window.location.href);
  const token = url.searchParams.get('token');
  if (!token) throw new Error('authentication_required');
  const response = await fetch('/api/v1/session/exchange', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token })
  });
  const session = await decode<components['schemas']['Session']>(response);
  csrfToken = session.csrfToken;
  scrubLaunchToken();
}

function scrubLaunchToken(): void {
  const url = new URL(window.location.href);
  if (!url.searchParams.has('token')) return;
  url.searchParams.delete('token');
  window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`);
}

export const api = {
  getVaultStatus: () => request<VaultStatus>('/api/v1/vault'),
  unlockVault: (method: 'operating-system' | 'passphrase', passphrase?: string) =>
    request<VaultStatus>('/api/v1/vault/unlock', { method: 'POST', body: JSON.stringify({ method, ...(passphrase ? { passphrase } : {}) }) }),
  listProfiles: () => request<ClusterProfile[]>('/api/v1/profiles'),
  createProfile: (input: ProfileInput) => request<ClusterProfile>('/api/v1/profiles', { method: 'POST', body: JSON.stringify(input) }),
  updateProfile: (id: string, input: ProfileInput) => request<ClusterProfile>(`/api/v1/profiles/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  getJourney: (profileId: string) => request<SetupJourney>(`/api/v1/profiles/${profileId}/journey`),
  getSettings: (profileId: string) => request<SetupSettings>(`/api/v1/profiles/${profileId}/settings`),
  getNodeTrust: (profileId: string) => request<NodeTrust>(`/api/v1/profiles/${profileId}/node-trust`),
  getOverlayIdentity: (profileId: string) => request<OverlayIdentity>(`/api/v1/profiles/${profileId}/overlay`),
  saveSettings: (profileId: string, settings: SetupSettings) => request<SetupSettings>(`/api/v1/profiles/${profileId}/settings`, { method: 'PUT', body: JSON.stringify(settings) }),
  listCredentials: (profileId: string) => request<CredentialMetadata[]>(`/api/v1/profiles/${profileId}/credentials`),
  storeCredential: (profileId: string, value: string, expiresAt: string) =>
    request<CredentialMetadata>(`/api/v1/profiles/${profileId}/credentials/git-provider-token`, {
      method: 'PUT',
      body: JSON.stringify({ value, expiresAt })
    }),
  removeCredential: (profileId: string) =>
    requestVoid(`/api/v1/profiles/${profileId}/credentials/git-provider-token`, { method: 'DELETE' }),
  exportRecoveryBundle: (profileId: string, encryption: { passphrase?: string; recipients?: string[] }) =>
    requestBinary('/api/v1/recovery-bundles/export', { method: 'POST', body: JSON.stringify({ profileId, ...encryption }) }),
  previewRecoveryBundle: (bundle: string, credential: { passphrase?: string; identity?: string }) =>
    request<RecoveryBundlePreview>('/api/v1/recovery-bundles/preview', { method: 'POST', body: JSON.stringify({ bundle, ...credential }) }),
  importRecoveryBundle: (bundle: string, expectedProfileId: string, credential: { passphrase?: string; identity?: string }) =>
    request<RecoveryBundleImportResult>('/api/v1/recovery-bundles/import', { method: 'POST', body: JSON.stringify({ bundle, expectedProfileId, ...credential }) }),
  getCapabilities: () => request<CapabilityCatalog>('/api/v1/capabilities'),
  planCapabilities: (input: { profileId: string; mode: CapabilityMode; communityIds: string[]; release: string; repositoryUrl: string; domain: string; environmentExtension?: string }) =>
    request<CapabilityPlanResult>('/api/v1/capabilities/plan', { method: 'POST', body: JSON.stringify(input) }),
  validateGitHubToken: (profileId: string, token: string, authority: 'creation' | 'ongoing') =>
    request<GitHubTokenStatus>('/api/v1/github/token/validate', { method: 'POST', body: JSON.stringify({ profileId, token, authority }) }),
  establishGitHubOverlay: (input: { profileId: string; planId: string; repositoryName: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string; environmentExtension?: string }) =>
    request<OverlayIdentity>('/api/v1/github/overlay/establish', { method: 'POST', body: JSON.stringify(input) }),
  validateGenericGitCredentials: (profileId: string, repositoryUrl: string, username: string, token: string) =>
    request<GenericGitCredentialStatus>('/api/v1/generic-git/token/validate', { method: 'POST', body: JSON.stringify({ profileId, repositoryUrl, username, token }) }),
  establishGenericGitOverlay: (input: { profileId: string; planId: string; repositoryUrl: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string; environmentExtension?: string }) =>
    request<OverlayIdentity>('/api/v1/generic-git/overlay/establish', { method: 'POST', body: JSON.stringify(input) }),
  proposeGenericGitOverlay: (input: { profileId: string; planId: string; repositoryUrl: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string; environmentExtension?: string }) =>
    request<GenericGitProposal>('/api/v1/generic-git/overlay/propose', { method: 'POST', body: JSON.stringify(input) }),
  getBootstrapAssetRequirements: (release: string) => request<BootstrapAssetRequirements>(`/api/v1/bootstrap-assets?release=${encodeURIComponent(release)}`),
  acquireBootstrapAssets: (release: string) => request<BootstrapAssetRequirements>('/api/v1/bootstrap-assets/acquire', { method: 'POST', body: JSON.stringify({ release }) }),
  getNodeCapabilities: () => request<NodeCapabilities>('/api/v1/nodes/capabilities'),
  probeNode: (profileId: string, target: NodeTarget) => request<NodeProbeResult>('/api/v1/nodes/probe', { method: 'POST', body: JSON.stringify({ profileId, target }) }),
  trustNode: (profileId: string, target: NodeTarget, fingerprint: string) => request<NodeProbeResult>('/api/v1/nodes/trust', { method: 'POST', body: JSON.stringify({ profileId, target, fingerprint, confirmed: true }) }),
  inspectNode: (profileId: string, target: NodeTarget, authentication: { kind: 'agent' | 'private-key' | 'password'; password?: string; privateKey?: string; keyPassphrase?: string; sudoPassword?: string }, dataDirectory: string) => request<NodeInspectionResult>('/api/v1/nodes/inspect', { method: 'POST', body: JSON.stringify({ profileId, target, authentication, dataDirectory }) }),
  cleanNode: (profileId: string, target: NodeTarget, authentication: { kind: 'agent' | 'private-key' | 'password'; password?: string; privateKey?: string; keyPassphrase?: string; sudoPassword?: string }, dataDirectory: string) => requestVoid('/api/v1/nodes/clean', { method: 'POST', body: JSON.stringify({ profileId, target, authentication, dataDirectory }) }),
  planNodeSSHKey: (profileId: string) => request<ChangePlan>('/api/v1/nodes/ssh-key/plan', { method: 'POST', body: JSON.stringify({ profileId }) }),
  planLocalBootstrap: (input: components['schemas']['LocalBootstrapPlanInput']) => request<LocalBootstrapPlanResult>('/api/v1/local-bootstrap/plan', { method: 'POST', body: JSON.stringify(input) }),
  createVerificationPlan: (profileId: string) => request<ChangePlan>('/api/v1/plans', { method: 'POST', body: JSON.stringify({ profileId, intent: 'VerifyLauncher' }) }),
  approvePlan: (planId: string) => request<WorkflowRun>(`/api/v1/plans/${planId}/approve`, { method: 'POST' }),
  getRun: (runId: string) => request<WorkflowRun>(`/api/v1/runs/${runId}`)
  ,cancelRun: (runId: string) => request<WorkflowRun>(`/api/v1/runs/${runId}/cancel`, { method: 'POST' }),
  establishClusterCA: (profileId: string) => request<ClusterCAReferenceView>('/api/v1/cluster-ca/establish', { method: 'POST', body: JSON.stringify({ profileId }) }),
  installClusterCADeviceTrust: (profileId: string) => request<ClusterCADeviceTrust>('/api/v1/cluster-ca/device-trust', { method: 'POST', body: JSON.stringify({ profileId }) }),
  establishPrivateNetwork: (profileId: string, baseDomain: string) => request<PrivateNetworkReferenceView>('/api/v1/private-network/establish', { method: 'POST', body: JSON.stringify({ profileId, baseDomain }) }),
  getTailscaleClient: () => request<TailscaleClientOffer>('/api/v1/tailscale-client'),
  establishEnrollment: (profileId: string) => request<EnrollmentReferenceView>('/api/v1/enrollment/establish', { method: 'POST', body: JSON.stringify({ profileId }) }),
  consumeLauncherEnrollment: (profileId: string) => request<EnrollmentReferenceView>('/api/v1/enrollment/launcher/consume', { method: 'POST', body: JSON.stringify({ profileId }) }),
  verifyHandoff: (profileId: string) => request<HandoffReport>('/api/v1/handoff/verify', { method: 'POST', body: JSON.stringify({ profileId }) }),
  closeTemporaryAccess: (profileId: string) => request<HandoffClosure>('/api/v1/handoff/close-temporary-access', { method: 'POST', body: JSON.stringify({ profileId }) }),
  claimFirstOwner: (profileId: string) => request<FirstOwnerState>('/api/v1/first-owner/claim', { method: 'POST', body: JSON.stringify({ profileId }) }),
  registerFirstOwner: (profileId: string, registration: { credentialId: string; clientDataJson: string; attestationObject: string }) =>
    request<FirstOwnerState>('/api/v1/first-owner/register', { method: 'POST', body: JSON.stringify({ profileId, ...registration }) }),
  getHandoffAssessment: (profileId: string) => request<HandoffAssessment>(`/api/v1/handoff-assessment?profileId=${encodeURIComponent(profileId)}`),
  getClusterDetail: (profileId: string) => request<ClusterDetail>(`/api/v1/cluster-detail?profileId=${encodeURIComponent(profileId)}`),
  getOffsiteProtection: (profileId: string) => request<OffsiteProtection>(`/api/v1/offsite?profileId=${encodeURIComponent(profileId)}`),
  inspectOffsiteDestination: (input: OffsiteDestinationInput) => request<OffsiteProtection>('/api/v1/offsite/inspect', { method: 'POST', body: JSON.stringify(input) }),
  planOffsiteProtection: (profileId: string, acknowledged: boolean) => request<OffsitePlan>('/api/v1/offsite/plan', { method: 'POST', body: JSON.stringify({ profileId, acknowledged }) }),
  proposeOffsiteProtection: (profileId: string, planId: string) => request<OffsiteProposal>('/api/v1/offsite/propose', { method: 'POST', body: JSON.stringify({ profileId, planId }) }),
  validateOffsiteProtection: (profileId: string) => request<{ plan: ChangePlan }>('/api/v1/offsite/validate', { method: 'POST', body: JSON.stringify({ profileId }) }),
  getHetznerProject: (profileId: string) => request<HetznerProject>(`/api/v1/hetzner?profileId=${encodeURIComponent(profileId)}`),
  // The token is sent once and custodied in the Launcher Vault; only the
  // fingerprint-bearing verdict comes back.
  validateHetznerToken: (profileId: string, token: string) => request<HetznerTokenAssessment>('/api/v1/hetzner/token/validate', { method: 'POST', body: JSON.stringify({ profileId, token }) }),
  inspectHetznerProject: (profileId: string, domain: string, envExt: string) => request<HetznerProject>('/api/v1/hetzner/inspect', { method: 'POST', body: JSON.stringify({ profileId, domain, envExt }) }),
  acquireHetznerToolchain: (profileId: string) => request<{ toolchain: HetznerToolchain; workspace: HetznerWorkspace }>('/api/v1/hetzner/toolchain/acquire', { method: 'POST', body: JSON.stringify({ profileId }) }),
  getHetznerPresets: (input: { profileId: string; mode: CapabilityMode; communityIds: string[]; location: string }) =>
    request<HetznerPresets>('/api/v1/hetzner/presets', { method: 'POST', body: JSON.stringify(input) }),
  planHetznerInfrastructure: (input: { profileId: string; mode: CapabilityMode; communityIds: string[]; tier: HetznerPresetTier; location: string; serverType?: string; volumeGb?: number; adoptions: string[]; acmeEmail: string }) =>
    request<HetznerPlanResult>('/api/v1/hetzner/plan', { method: 'POST', body: JSON.stringify(input) }),
  // Narrowing cannot reopen a closed path, and an address that would produce a
  // rule admitting nobody leaves it open with the reason stated instead.
  narrowHetznerTemporaryAccess: (profileId: string, operatorAddress: string) =>
    request<TemporaryAccess>('/api/v1/hetzner/temporary-access/narrow', { method: 'POST', body: JSON.stringify({ profileId, operatorAddress }) }),
  inspectPreserveDataDecommission: (profileId: string) =>
    request<{ inspection: unknown; preview: PreserveDataDecommissionPlan }>(`/api/v1/decommission?profileId=${encodeURIComponent(profileId)}`),
  planPreserveDataDecommission: (profileId: string) =>
    request<PreserveDataDecommissionResult>('/api/v1/decommission/plan', { method: 'POST', body: JSON.stringify({ profileId }) }),
  resumePreserveDataDecommission: (runId: string) =>
    request<WorkflowRun>(`/api/v1/decommission/runs/${encodeURIComponent(runId)}/resume`, { method: 'POST' }),
  inspectFullDecommission: (profileId: string) =>
    request<{ inspection: unknown; preview: FullDecommissionPlan }>(`/api/v1/full-decommission?profileId=${encodeURIComponent(profileId)}`),
  planFullDecommission: (profileId: string) =>
    request<FullDecommissionResult>('/api/v1/full-decommission/plan', { method: 'POST', body: JSON.stringify({ profileId }) }),
  approveFullDecommission: (input: { planId: string; profileId: string; planDigest: string; confirmation: string; ownerOverride: boolean; overrideReason: string }) =>
    request<WorkflowRun>('/api/v1/full-decommission/approve', { method: 'POST', body: JSON.stringify(input) }),
  resumeFullDecommission: (runId: string) =>
    request<WorkflowRun>(`/api/v1/full-decommission/runs/${encodeURIComponent(runId)}/resume`, { method: 'POST' }),
  exportFullDecommissionActivity: (profileId: string) =>
    request<{ profileId: string; redacted: true; activity: Array<{ type: string; messageKey: string; occurredAt: string }> }>(`/api/v1/full-decommission/activity?profileId=${encodeURIComponent(profileId)}`),
  forgetProfile: (profileId: string) =>
    request<{ profileId: string; forgotten: boolean; externalMutation: false }>(`/api/v1/profiles/${encodeURIComponent(profileId)}/forget`, { method: 'POST', body: JSON.stringify({ confirmProfileId: profileId }) })
};
