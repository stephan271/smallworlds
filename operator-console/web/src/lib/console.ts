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

export class ConsoleApiError extends Error {
  constructor(public readonly status: number) {
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

export const consoleApi = {
  loginPath: '/api/v1/auth/login',
  session: () => getJSON<Session>('/api/v1/session'),
  overview: () => getJSON<Overview>('/api/v1/overview'),
  capability: (id: string) =>
    getJSON<CapabilityAssessment>(`/api/v1/capabilities/${encodeURIComponent(id)}`),
  logout: () => fetch('/api/v1/auth/logout', { method: 'POST' })
};

export function hasPermission(session: Session | null, permission: Permission): boolean {
  return !!session?.permissions?.includes(permission);
}
