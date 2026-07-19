import type { components } from './generated/api';

export type ClusterProfile = components['schemas']['ClusterProfile'];
export type ProfileInput = components['schemas']['ProfileInput'];
export type SetupJourney = components['schemas']['SetupJourney'];
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
  planCapabilities: (input: { profileId: string; mode: CapabilityMode; communityIds: string[]; release: string; repositoryUrl: string; domain: string }) =>
    request<CapabilityPlanResult>('/api/v1/capabilities/plan', { method: 'POST', body: JSON.stringify(input) }),
  validateGitHubToken: (profileId: string, token: string, authority: 'creation' | 'ongoing') =>
    request<GitHubTokenStatus>('/api/v1/github/token/validate', { method: 'POST', body: JSON.stringify({ profileId, token, authority }) }),
  establishGitHubOverlay: (input: { profileId: string; planId: string; repositoryName: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string }) =>
    request<{ repositoryUrl: string; commit: string }>('/api/v1/github/overlay/establish', { method: 'POST', body: JSON.stringify(input) }),
  validateGenericGitCredentials: (profileId: string, repositoryUrl: string, username: string, token: string) =>
    request<GenericGitCredentialStatus>('/api/v1/generic-git/token/validate', { method: 'POST', body: JSON.stringify({ profileId, repositoryUrl, username, token }) }),
  establishGenericGitOverlay: (input: { profileId: string; planId: string; repositoryUrl: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string }) =>
    request<{ repositoryUrl: string; commit: string }>('/api/v1/generic-git/overlay/establish', { method: 'POST', body: JSON.stringify(input) }),
  proposeGenericGitOverlay: (input: { profileId: string; planId: string; repositoryUrl: string; mode: CapabilityMode; communityIds: string[]; release: string; domain: string }) =>
    request<GenericGitProposal>('/api/v1/generic-git/overlay/propose', { method: 'POST', body: JSON.stringify(input) }),
  createVerificationPlan: (profileId: string) => request<ChangePlan>('/api/v1/plans', { method: 'POST', body: JSON.stringify({ profileId, intent: 'VerifyLauncher' }) }),
  approvePlan: (planId: string) => request<WorkflowRun>(`/api/v1/plans/${planId}/approve`, { method: 'POST' }),
  getRun: (runId: string) => request<WorkflowRun>(`/api/v1/runs/${runId}`)
};
