import type { components } from './generated/api';

export type ClusterProfile = components['schemas']['ClusterProfile'];
export type ProfileInput = components['schemas']['ProfileInput'];
export type SetupJourney = components['schemas']['SetupJourney'];
export type ChangePlan = components['schemas']['ChangePlan'];
export type WorkflowRun = components['schemas']['WorkflowRun'];
export type VaultStatus = components['schemas']['VaultStatus'];
export type CredentialMetadata = components['schemas']['CredentialMetadata'];

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
  createVerificationPlan: (profileId: string) => request<ChangePlan>('/api/v1/plans', { method: 'POST', body: JSON.stringify({ profileId, intent: 'VerifyLauncher' }) }),
  approvePlan: (planId: string) => request<WorkflowRun>(`/api/v1/plans/${planId}/approve`, { method: 'POST' }),
  getRun: (runId: string) => request<WorkflowRun>(`/api/v1/runs/${runId}`)
};
