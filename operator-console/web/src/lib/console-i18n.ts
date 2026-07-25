// Localization for the in-cluster Operator Console screens, isolated from the
// launcher's catalog. Backend reason codes are the interface contract; this
// module maps them to translated, operator-facing text. English is canonical;
// `de` is typed as Record<ConsoleMessageKey, string>, so svelte-check fails the
// build if the German catalog drifts from the English keys.

import type {
  CapabilityState,
  ConsoleRole,
  DataType,
  FacetKind,
  FacetState,
  ProtectionLevel
} from './console';

export type Locale = 'en' | 'de';

const en = {
  consoleTitle: 'Operator Console',
  consoleTagline: 'Understand your cluster at a glance.',
  signIn: 'Sign in',
  signInPrompt: 'Sign in with your cluster identity to view Capability Assessments.',
  signOut: 'Sign out',
  signedInAs: 'Signed in as',
  notSignedIn: 'Not signed in',
  roleLabel: 'Role',
  languageLabel: 'Language',
  loading: 'Loading…',
  loadError: 'The console could not be loaded.',
  retry: 'Try again',
  forbidden: 'Access denied',
  forbiddenDetail: 'Your account has no Console Role. Ask an Owner for access.',
  overviewHeading: 'Cluster overview',
  capabilitiesHeading: 'Capabilities',
  noCapabilities: 'No capabilities to show.',
  selectCapabilityHint: 'Select a capability to see its evidence.',
  backToOverview: 'Back to overview',
  headlineLabel: 'Capability state',
  facetsHeading: 'Evidence facets',
  evidenceObserved: 'Observed',
  evidenceStale: 'Stale evidence',
  evidenceNever: 'Never observed',
  remediationLabel: 'Next step',
  routeSetup: 'Open setup',
  routeProposal: 'Propose a change',
  routeRuntimeAction: 'Run action',
  routeDocs: 'Open documentation',
  routeGrafana: 'Open in Grafana',
  routeArgo: 'Open in Argo CD',
  opensNewTab: 'opens in a new tab',

  navCapabilities: 'Capabilities',
  navProtection: 'Protection',
  protectionHeading: 'Dataset protection',
  protectionIntro:
    'Each dataset shows its producer Job, its local (in-cluster Garage) Recovery Point, and its offsite Recovery Point separately — a Job completing is not proof of a restorable point.',
  protectionRoadmap:
    'Restore, backup deletion, and retention changes are not available in this release; they are shown here only as planned work.',
  disasterProtectedYes: 'Disaster protected',
  disasterProtectedNo: 'Not disaster protected',
  localOnlyWarning: 'Local Recovery Point only — same disk as the primary data, so not disaster protection.',
  colOwner: 'Capability',
  colType: 'Data type',
  colProducer: 'Producer',
  colSchedule: 'Schedule',
  colRetention: 'Retention',
  colJob: 'Producer Job',
  colLocalRP: 'Local Recovery Point',
  colOffsiteRP: 'Offsite Recovery Point',
  colRestoreDrill: 'Restore Drill',
  jobFailedLabel: 'Last Job failed',
  jobNever: 'No Job recorded',
  noRecoveryPoint: 'None',
  staleSuffix: '(stale)',
  restoreDrillNone: 'No Restore Drill recorded',
  restoreDrillPassed: 'passed',
  restoreDrillFailed: 'failed',
  notObserved: 'Evidence unavailable',

  navAdditions: 'Add application',
  additionsHeading: 'Add a community application',
  additionsIntro:
    'Enable one more community application by opening a Git proposal against your overlay. The console never changes your cluster directly — you review and merge the proposal, and the new application then appears here with its own evidence.',
  additionsEmpty: 'There are no community applications available to add right now.',
  addPlanButton: 'Plan this addition',
  offerAlsoEnables: 'Also enables',
  offerStores: 'Stores data',
  planHeading: 'Review the change',
  planBackToOffers: 'Back to applications',
  planAddsLabel: 'This proposal adds',
  planPresentLabel: 'Already present',
  planResourcesHeading: 'Estimated resources vs. capacity',
  planMemory: 'Memory',
  planStorage: 'Storage',
  planNeeded: 'Needed',
  planAvailable: 'Available',
  planFitsYes: 'Fits current capacity',
  planFitsNo: 'Exceeds available capacity — add resources before merging',
  planExposureLabel: 'Exposure',
  planDataLabel: 'Persistent data',
  planDataNone: 'None',
  planProtectionLabel: 'Protection',
  planDiffHeading: 'Exact Git changes',
  planDiffHint:
    'These files are added to your GitOps overlay. No live Kubernetes resources are changed directly.',
  approveButton: 'Approve this plan',
  proposeButton: 'Open Git proposal',
  proposalOpenedHeading: 'Proposal opened',
  proposalMergeObserved:
    'The console will not merge this for you. Review and merge the pull request; the new application then follows its Argo, runtime, access, and protection evidence here.',
  proposalProvider: 'Provider',
  proposalBranch: 'Branch',
  proposalCommit: 'Commit',
  proposalOpenLink: 'Open pull request',
  addAnother: 'Add another application',
  additionErrorGeneric: 'The change could not be completed.',
  additionErrorMismatch: 'The cluster changed since you planned. Re-plan to continue.',
  additionErrorCapacity: 'Live capacity is not available yet, so additions cannot be planned.',
  additionErrorProposal: 'The overlay proposal path is not configured yet.',
  additionErrorNotOffered: 'This application can no longer be added.',

  navUpdates: 'Updates',
  updatesHeading: 'SmallWorlds release update',
  updatesIntro:
    'Inspect signed release metadata and compatibility before proposing an explicit GitOps update. Nothing is installed or merged automatically.',
  updateProfileHeading: 'Cluster profile',
  updateExportProfile: 'Export profile',
  updateCurrentRelease: 'Current base tag',
  updateLauncherVersion: 'Launcher',
  updateClusterVersion: 'Cluster',
  updateCatalogVersion: 'Catalog',
  updateNoRelease: 'No newer signed release is available.',
  updateAvailableHeading: 'Signed release available',
  updateSignatureValid: 'Release signature verified',
  updateCompatibilityYes: 'Compatible with this launcher and cluster',
  updateCompatibilityNo: 'Incompatible — inspection and profile export remain available, but planning is blocked',
  updatePlanButton: 'Review update plan',
  updateNotesHeading: 'Release notes',
  updateCapabilitiesHeading: 'Capability changes',
  updatePinsHeading: 'Immutable image and tool pins',
  updateRisksHeading: 'Operational risks',
  updateDowntimeRisks: 'Downtime',
  updateDataRisks: 'Data',
  updateExposureRisks: 'Exposure',
  updateRecoveryHeading: 'Recovery expectation',
  updateProposalSafety:
    'The proposal creates a branch and pull request only. It does not merge, force-push, or mutate the live cluster.',
  updateProposalOpened: 'Release proposal opened',
  updateAdoptionHeading: 'Post-merge adoption evidence',
  updateRefreshAdoption: 'Refresh adoption evidence',
  updateArgoEvidence: 'Argo convergence',
  updateAdoptionUnavailable: 'Adoption evidence is not available yet.',
  updateErrorGeneric: 'The release update could not be completed.',
  updateErrorIncompatible: 'This launcher, cluster, or catalog is outside the signed compatibility range.',
  updateErrorMismatch: 'The cluster or signed release changed since approval. Create a new plan.',

  navAccess: 'Access',
  accessHeading: 'Operator device access',
  accessIntro:
    'Enroll additional Operator Devices with short-lived, single-use invitations, and revoke a lost device through an inspected, approved action — no cluster shell or Headscale administration required.',
  accessDevicesHeading: 'Current devices',
  accessSummary: 'devices',
  accessOwnerDevices: 'with Owner access',
  accessNoDevices: 'No devices are enrolled yet.',
  deviceOwnerBadge: 'Owner access',
  deviceSelfBadge: 'This device',
  deviceOnline: 'Online',
  deviceOffline: 'Offline',
  deviceLastSeen: 'Last seen',
  deviceNever: 'never',

  enrollHeading: 'Enroll a device',
  enrollIntro:
    'Create a short-lived, single-use invitation. The join key is shown once and is not a reusable cluster or Headscale administrator credential.',
  enrollLabelLabel: 'Device label',
  enrollLabelPlaceholder: 'e.g. alice-laptop',
  enrollCreateButton: 'Create invitation',
  enrollGuidanceHeading: 'How this device joins',
  enrollElevationBadge: 'Needs elevation',
  enrollCaRequired: 'This deployment requires installing the Cluster CA root on the device.',
  enrollGatewayLabel: 'Private Gateway',
  enrollHostsLabel: 'Operator hostnames (reachable only through the gateway)',
  enrollJoinKeyHeading: 'Single-use join key',
  enrollJoinKeyOnce:
    'Copy this now — it is shown once. The invitation is single-use and expires soon.',
  enrollExpiresAt: 'Expires',
  enrollIssuedBy: 'Issued by',
  enrollAnother: 'Create another invitation',

  'step_acquire-tailscale-client': 'Install the official Tailscale client (verified download)',
  'step_join-private-network': 'Join the Private Network with the single-use invitation',
  'step_configure-private-dns': 'Use the Private Network DNS so operator hostnames resolve',
  'step_install-cluster-ca': 'Install the Cluster CA root so operator interfaces are trusted',
  'step_verify-gateway-access': 'Confirm operator hostnames open through the Private Gateway',

  revokeHeading: 'Revoke a device',
  revokeIntro:
    'Removing a lost device is an inspected, approved action. Its lockout impact is shown before you approve.',
  revokeSelectPrompt: 'Select a device above and plan its revocation.',
  revokePlanButton: 'Plan revocation',
  revokeAssessmentHeading: 'Revocation impact',
  revokeAffected: 'Affected device',
  revokeAlternativeYes: 'Other Owner devices remain',
  revokeAlternativeNo: 'No other Owner device would remain',
  revokeRemaining: 'Remaining Owner devices',
  revokeLockoutWarning: 'Lockout risk',
  'lockout_last-owner-device': 'This is the last device with Owner access — removing it risks a lockout.',
  'lockout_self-revocation': 'This is the device you are using — revoking it may cut off your session.',
  revokeApproveButton: 'Approve revocation',
  revokeExecuteButton: 'Revoke device',
  revokeDoneHeading: 'Device revoked',
  revokeAccessVerified: 'Loss of access was verified.',
  revokeAccessNotVerified: 'The device was removed, but loss of access could not be verified.',
  revokeAnother: 'Back to devices',

  activityHeading: 'Recent access activity',
  activityEmpty: 'No device access activity recorded yet.',

  deviceErrorGeneric: 'The action could not be completed.',
  deviceErrorDirectory: 'The device directory is not available yet.',
  deviceErrorInvitation: 'The invitation path is not configured yet.',
  deviceErrorRevocation: 'The device revocation path is not configured yet.',
  deviceErrorMismatch: 'The devices changed since you planned. Re-plan to continue.',
  deviceErrorNotApproved: 'Approve the plan before revoking.',
  deviceErrorNotFound: 'That device is no longer present.',

  'cat_application-policy': 'Application-defined access policy',
  'cat_private-gateway': 'Private Gateway only',
  'cat_capability-backup': 'Per-application backup',
  'cat_cluster-backup': 'Cluster-wide backup',

  plevel_unknown: 'Unknown',
  plevel_none: 'No protection',
  'plevel_local-only': 'Local only',
  plevel_stale: 'Stale',
  plevel_protected: 'Protected',

  dtype_database: 'Database',
  dtype_filesystem: 'Filesystem',
  'dtype_object-store': 'Object store',
  'dtype_cluster-resources': 'Cluster resources',

  role_observer: 'Observer',
  role_operator: 'Operator',
  role_owner: 'Owner',
  role_none: 'No role',

  state_planned: 'Planned',
  state_blocked: 'Blocked',
  state_installing: 'Installing',
  state_healthy: 'Healthy',
  state_degraded: 'Degraded',
  state_failed: 'Failed',
  state_disabled: 'Disabled',

  facet_configuration: 'Configuration',
  facet_delivery: 'Delivery',
  facet_runtime: 'Runtime',
  facet_access: 'Access',
  facet_protection: 'Protection',

  fstate_satisfied: 'Satisfied',
  fstate_pending: 'Pending',
  fstate_progressing: 'In progress',
  fstate_degraded: 'Degraded',
  fstate_failed: 'Failed',
  fstate_blocked: 'Blocked',
  fstate_unknown: 'Unknown',
  fstate_not_applicable: 'Not applicable',

  reasonUnknown: 'Unknown state',
  healthy: 'Healthy',
  'awaiting-delivery': 'Awaiting delivery',
  'configuration-evidence-unknown': 'Configuration evidence unavailable',
  'capability-disabled': 'Not selected',
  'dependency-unmet': 'A required dependency is not ready',
  'required-values-missing': 'Required configuration values are missing',
  'not-declared-in-git': 'Not yet declared in the GitOps overlay',
  'delivery-evidence-unknown': 'Argo CD evidence unavailable',
  'delivery-failed': 'Argo CD reports the application degraded',
  'delivery-progressing': 'Argo CD is reconciling',
  'delivery-pending': 'Argo CD has not created the application yet',
  'delivery-drifted': 'The live application has drifted from Git',
  'delivery-out-of-sync': 'The application is out of sync',
  'runtime-evidence-unknown': 'Runtime evidence unavailable',
  'runtime-jobs-failed': 'One or more Jobs have failed',
  'runtime-pvc-unbound': 'A persistent volume claim is unbound',
  'runtime-workloads-not-ready': 'Workloads are not ready',
  'runtime-probes-failing': 'Application probes are failing',
  'access-evidence-unknown': 'Access evidence unavailable',
  'access-dns-unresolved': 'DNS does not resolve',
  'access-certificate-not-ready': 'The TLS certificate is not ready',
  'access-public-unreachable': 'Not reachable over public ingress',
  'access-gateway-unreachable': 'Not reachable through the Private Gateway',
  'access-private-exposed-publicly': 'A private capability is reachable from the public internet',
  'access-internal-exposed': 'An internal capability is exposed to ingress',
  'protection-not-applicable': 'No datasets to protect',
  'protection-evidence-unknown': 'Protection evidence unavailable',
  'protection-coverage-gap': 'Some datasets are not covered by backups',
  'protection-no-local-recovery-point': 'No local recovery point exists',
  'protection-local-recovery-point-stale': 'The local recovery point is stale',
  'protection-no-offsite-recovery-point': 'No offsite recovery point exists',
  'protection-offsite-recovery-point-stale': 'The offsite recovery point is stale',
  'protection-retention-unmet': 'Retention policy is not satisfied'
} as const;

export type ConsoleMessageKey = keyof typeof en;

const de: Record<ConsoleMessageKey, string> = {
  consoleTitle: 'Operator-Console',
  consoleTagline: 'Verstehen Sie Ihren Cluster auf einen Blick.',
  signIn: 'Anmelden',
  signInPrompt: 'Melden Sie sich mit Ihrer Cluster-Identität an, um Fähigkeitsbewertungen zu sehen.',
  signOut: 'Abmelden',
  signedInAs: 'Angemeldet als',
  notSignedIn: 'Nicht angemeldet',
  roleLabel: 'Rolle',
  languageLabel: 'Sprache',
  loading: 'Wird geladen…',
  loadError: 'Die Console konnte nicht geladen werden.',
  retry: 'Erneut versuchen',
  forbidden: 'Zugriff verweigert',
  forbiddenDetail: 'Ihr Konto hat keine Console-Rolle. Bitten Sie einen Eigentümer um Zugriff.',
  overviewHeading: 'Cluster-Übersicht',
  capabilitiesHeading: 'Fähigkeiten',
  noCapabilities: 'Keine Fähigkeiten anzuzeigen.',
  selectCapabilityHint: 'Wählen Sie eine Fähigkeit, um ihre Nachweise zu sehen.',
  backToOverview: 'Zurück zur Übersicht',
  headlineLabel: 'Fähigkeitszustand',
  facetsHeading: 'Nachweis-Facetten',
  evidenceObserved: 'Beobachtet',
  evidenceStale: 'Veralteter Nachweis',
  evidenceNever: 'Nie beobachtet',
  remediationLabel: 'Nächster Schritt',
  routeSetup: 'Einrichtung öffnen',
  routeProposal: 'Änderung vorschlagen',
  routeRuntimeAction: 'Aktion ausführen',
  routeDocs: 'Dokumentation öffnen',
  routeGrafana: 'In Grafana öffnen',
  routeArgo: 'In Argo CD öffnen',
  opensNewTab: 'öffnet in einem neuen Tab',

  navCapabilities: 'Fähigkeiten',
  navProtection: 'Schutz',
  protectionHeading: 'Datensatzschutz',
  protectionIntro:
    'Jeder Datensatz zeigt seinen Producer-Job, seinen lokalen (clusterinternen Garage-)Wiederherstellungspunkt und seinen externen Wiederherstellungspunkt getrennt — ein abgeschlossener Job ist kein Nachweis eines wiederherstellbaren Punkts.',
  protectionRoadmap:
    'Wiederherstellung, Backup-Löschung und Aufbewahrungsänderungen sind in dieser Version nicht verfügbar; sie werden hier nur als geplante Arbeit gezeigt.',
  disasterProtectedYes: 'Katastrophengeschützt',
  disasterProtectedNo: 'Nicht katastrophengeschützt',
  localOnlyWarning:
    'Nur lokaler Wiederherstellungspunkt — dieselbe Festplatte wie die Primärdaten, also kein Katastrophenschutz.',
  colOwner: 'Fähigkeit',
  colType: 'Datentyp',
  colProducer: 'Producer',
  colSchedule: 'Zeitplan',
  colRetention: 'Aufbewahrung',
  colJob: 'Producer-Job',
  colLocalRP: 'Lokaler Wiederherstellungspunkt',
  colOffsiteRP: 'Externer Wiederherstellungspunkt',
  colRestoreDrill: 'Wiederherstellungsübung',
  jobFailedLabel: 'Letzter Job fehlgeschlagen',
  jobNever: 'Kein Job erfasst',
  noRecoveryPoint: 'Keiner',
  staleSuffix: '(veraltet)',
  restoreDrillNone: 'Keine Wiederherstellungsübung erfasst',
  restoreDrillPassed: 'bestanden',
  restoreDrillFailed: 'fehlgeschlagen',
  notObserved: 'Nachweis nicht verfügbar',

  navAdditions: 'Anwendung hinzufügen',
  additionsHeading: 'Community-Anwendung hinzufügen',
  additionsIntro:
    'Aktivieren Sie eine weitere Community-Anwendung, indem Sie einen Git-Vorschlag für Ihr Overlay öffnen. Die Console ändert Ihren Cluster nie direkt — Sie prüfen und mergen den Vorschlag, und die neue Anwendung erscheint dann hier mit ihren eigenen Nachweisen.',
  additionsEmpty: 'Derzeit sind keine Community-Anwendungen zum Hinzufügen verfügbar.',
  addPlanButton: 'Hinzufügen planen',
  offerAlsoEnables: 'Aktiviert außerdem',
  offerStores: 'Speichert Daten',
  planHeading: 'Änderung prüfen',
  planBackToOffers: 'Zurück zu den Anwendungen',
  planAddsLabel: 'Dieser Vorschlag fügt hinzu',
  planPresentLabel: 'Bereits vorhanden',
  planResourcesHeading: 'Geschätzte Ressourcen vs. Kapazität',
  planMemory: 'Arbeitsspeicher',
  planStorage: 'Speicher',
  planNeeded: 'Benötigt',
  planAvailable: 'Verfügbar',
  planFitsYes: 'Passt in die aktuelle Kapazität',
  planFitsNo: 'Übersteigt die verfügbare Kapazität — vor dem Merge Ressourcen ergänzen',
  planExposureLabel: 'Erreichbarkeit',
  planDataLabel: 'Persistente Daten',
  planDataNone: 'Keine',
  planProtectionLabel: 'Schutz',
  planDiffHeading: 'Genaue Git-Änderungen',
  planDiffHint:
    'Diese Dateien werden zu Ihrem GitOps-Overlay hinzugefügt. Keine laufenden Kubernetes-Ressourcen werden direkt geändert.',
  approveButton: 'Plan genehmigen',
  proposeButton: 'Git-Vorschlag öffnen',
  proposalOpenedHeading: 'Vorschlag geöffnet',
  proposalMergeObserved:
    'Die Console merged dies nicht für Sie. Prüfen und mergen Sie den Pull Request; die neue Anwendung folgt dann hier ihren Argo-, Laufzeit-, Zugriffs- und Schutznachweisen.',
  proposalProvider: 'Anbieter',
  proposalBranch: 'Branch',
  proposalCommit: 'Commit',
  proposalOpenLink: 'Pull Request öffnen',
  addAnother: 'Weitere Anwendung hinzufügen',
  additionErrorGeneric: 'Die Änderung konnte nicht abgeschlossen werden.',
  additionErrorMismatch:
    'Der Cluster hat sich seit Ihrer Planung geändert. Bitte neu planen, um fortzufahren.',
  additionErrorCapacity:
    'Die Live-Kapazität ist noch nicht verfügbar, daher können keine Ergänzungen geplant werden.',
  additionErrorProposal: 'Der Overlay-Vorschlagspfad ist noch nicht konfiguriert.',
  additionErrorNotOffered: 'Diese Anwendung kann nicht mehr hinzugefügt werden.',

  navUpdates: 'Aktualisierungen',
  updatesHeading: 'SmallWorlds-Versionsaktualisierung',
  updatesIntro:
    'Prüfen Sie signierte Versionsmetadaten und Kompatibilität vor einem expliziten GitOps-Aktualisierungsvorschlag. Nichts wird automatisch installiert oder gemergt.',
  updateProfileHeading: 'Clusterprofil',
  updateExportProfile: 'Profil exportieren',
  updateCurrentRelease: 'Aktueller Basis-Tag',
  updateLauncherVersion: 'Launcher',
  updateClusterVersion: 'Cluster',
  updateCatalogVersion: 'Katalog',
  updateNoRelease: 'Keine neuere signierte Version ist verfügbar.',
  updateAvailableHeading: 'Signierte Version verfügbar',
  updateSignatureValid: 'Versionssignatur verifiziert',
  updateCompatibilityYes: 'Mit diesem Launcher und Cluster kompatibel',
  updateCompatibilityNo:
    'Inkompatibel — Prüfung und Profilexport bleiben verfügbar, die Planung ist jedoch gesperrt',
  updatePlanButton: 'Aktualisierungsplan prüfen',
  updateNotesHeading: 'Versionshinweise',
  updateCapabilitiesHeading: 'Fähigkeitsänderungen',
  updatePinsHeading: 'Unveränderliche Image- und Werkzeug-Pins',
  updateRisksHeading: 'Betriebsrisiken',
  updateDowntimeRisks: 'Ausfallzeit',
  updateDataRisks: 'Daten',
  updateExposureRisks: 'Erreichbarkeit',
  updateRecoveryHeading: 'Wiederherstellungserwartung',
  updateProposalSafety:
    'Der Vorschlag erstellt nur einen Branch und Pull Request. Er merged nicht, erzwingt keinen Push und verändert den laufenden Cluster nicht.',
  updateProposalOpened: 'Versionsvorschlag geöffnet',
  updateAdoptionHeading: 'Nachweise zur Übernahme nach dem Merge',
  updateRefreshAdoption: 'Übernahmenachweise aktualisieren',
  updateArgoEvidence: 'Argo-Konvergenz',
  updateAdoptionUnavailable: 'Übernahmenachweise sind noch nicht verfügbar.',
  updateErrorGeneric: 'Die Versionsaktualisierung konnte nicht abgeschlossen werden.',
  updateErrorIncompatible:
    'Dieser Launcher, Cluster oder Katalog liegt außerhalb des signierten Kompatibilitätsbereichs.',
  updateErrorMismatch:
    'Der Cluster oder die signierte Version hat sich seit der Genehmigung geändert. Erstellen Sie einen neuen Plan.',

  navAccess: 'Zugriff',
  accessHeading: 'Zugriff für Operator-Geräte',
  accessIntro:
    'Registrieren Sie zusätzliche Operator-Geräte mit kurzlebigen Einmal-Einladungen und widerrufen Sie ein verlorenes Gerät über eine geprüfte, genehmigte Aktion — ohne Cluster-Shell oder Headscale-Administration.',
  accessDevicesHeading: 'Aktuelle Geräte',
  accessSummary: 'Geräte',
  accessOwnerDevices: 'mit Eigentümer-Zugriff',
  accessNoDevices: 'Es sind noch keine Geräte registriert.',
  deviceOwnerBadge: 'Eigentümer-Zugriff',
  deviceSelfBadge: 'Dieses Gerät',
  deviceOnline: 'Online',
  deviceOffline: 'Offline',
  deviceLastSeen: 'Zuletzt gesehen',
  deviceNever: 'nie',

  enrollHeading: 'Gerät registrieren',
  enrollIntro:
    'Erstellen Sie eine kurzlebige Einmal-Einladung. Der Beitrittsschlüssel wird einmal angezeigt und ist kein wiederverwendbares Cluster- oder Headscale-Administrator-Anmeldedatum.',
  enrollLabelLabel: 'Gerätebezeichnung',
  enrollLabelPlaceholder: 'z. B. alice-laptop',
  enrollCreateButton: 'Einladung erstellen',
  enrollGuidanceHeading: 'So tritt dieses Gerät bei',
  enrollElevationBadge: 'Benötigt Rechteerhöhung',
  enrollCaRequired:
    'Diese Bereitstellung erfordert die Installation des Cluster-CA-Stammzertifikats auf dem Gerät.',
  enrollGatewayLabel: 'Private Gateway',
  enrollHostsLabel: 'Operator-Hostnamen (nur über das Gateway erreichbar)',
  enrollJoinKeyHeading: 'Einmaliger Beitrittsschlüssel',
  enrollJoinKeyOnce:
    'Jetzt kopieren — er wird nur einmal angezeigt. Die Einladung ist einmalig und läuft bald ab.',
  enrollExpiresAt: 'Läuft ab',
  enrollIssuedBy: 'Ausgestellt von',
  enrollAnother: 'Weitere Einladung erstellen',

  'step_acquire-tailscale-client': 'Offiziellen Tailscale-Client installieren (verifizierter Download)',
  'step_join-private-network': 'Mit der Einmal-Einladung dem Private Network beitreten',
  'step_configure-private-dns': 'Private-Network-DNS nutzen, damit Operator-Hostnamen auflösen',
  'step_install-cluster-ca': 'Cluster-CA-Stammzertifikat installieren, damit Operator-Oberflächen vertraut werden',
  'step_verify-gateway-access': 'Bestätigen, dass Operator-Hostnamen über das Private Gateway öffnen',

  revokeHeading: 'Gerät widerrufen',
  revokeIntro:
    'Das Entfernen eines verlorenen Geräts ist eine geprüfte, genehmigte Aktion. Die Aussperrungs-Auswirkung wird vor der Genehmigung angezeigt.',
  revokeSelectPrompt: 'Wählen Sie oben ein Gerät und planen Sie dessen Widerruf.',
  revokePlanButton: 'Widerruf planen',
  revokeAssessmentHeading: 'Auswirkung des Widerrufs',
  revokeAffected: 'Betroffenes Gerät',
  revokeAlternativeYes: 'Weitere Eigentümer-Geräte bleiben bestehen',
  revokeAlternativeNo: 'Es würde kein weiteres Eigentümer-Gerät verbleiben',
  revokeRemaining: 'Verbleibende Eigentümer-Geräte',
  revokeLockoutWarning: 'Aussperrungsrisiko',
  'lockout_last-owner-device':
    'Dies ist das letzte Gerät mit Eigentümer-Zugriff — sein Entfernen riskiert eine Aussperrung.',
  'lockout_self-revocation':
    'Dies ist das Gerät, das Sie verwenden — sein Widerruf kann Ihre Sitzung trennen.',
  revokeApproveButton: 'Widerruf genehmigen',
  revokeExecuteButton: 'Gerät widerrufen',
  revokeDoneHeading: 'Gerät widerrufen',
  revokeAccessVerified: 'Der Zugriffsverlust wurde bestätigt.',
  revokeAccessNotVerified:
    'Das Gerät wurde entfernt, aber der Zugriffsverlust konnte nicht bestätigt werden.',
  revokeAnother: 'Zurück zu den Geräten',

  activityHeading: 'Letzte Zugriffsaktivität',
  activityEmpty: 'Noch keine Geräte-Zugriffsaktivität erfasst.',

  deviceErrorGeneric: 'Die Aktion konnte nicht abgeschlossen werden.',
  deviceErrorDirectory: 'Das Geräteverzeichnis ist noch nicht verfügbar.',
  deviceErrorInvitation: 'Der Einladungspfad ist noch nicht konfiguriert.',
  deviceErrorRevocation: 'Der Geräte-Widerrufspfad ist noch nicht konfiguriert.',
  deviceErrorMismatch: 'Die Geräte haben sich seit Ihrer Planung geändert. Bitte neu planen.',
  deviceErrorNotApproved: 'Genehmigen Sie den Plan vor dem Widerruf.',
  deviceErrorNotFound: 'Dieses Gerät ist nicht mehr vorhanden.',

  'cat_application-policy': 'Anwendungsdefinierte Zugriffsrichtlinie',
  'cat_private-gateway': 'Nur Private Gateway',
  'cat_capability-backup': 'Anwendungsspezifisches Backup',
  'cat_cluster-backup': 'Clusterweites Backup',

  plevel_unknown: 'Unbekannt',
  plevel_none: 'Kein Schutz',
  'plevel_local-only': 'Nur lokal',
  plevel_stale: 'Veraltet',
  plevel_protected: 'Geschützt',

  dtype_database: 'Datenbank',
  dtype_filesystem: 'Dateisystem',
  'dtype_object-store': 'Objektspeicher',
  'dtype_cluster-resources': 'Cluster-Ressourcen',

  role_observer: 'Beobachter',
  role_operator: 'Operator',
  role_owner: 'Eigentümer',
  role_none: 'Keine Rolle',

  state_planned: 'Geplant',
  state_blocked: 'Blockiert',
  state_installing: 'Wird installiert',
  state_healthy: 'Gesund',
  state_degraded: 'Beeinträchtigt',
  state_failed: 'Fehlgeschlagen',
  state_disabled: 'Deaktiviert',

  facet_configuration: 'Konfiguration',
  facet_delivery: 'Auslieferung',
  facet_runtime: 'Laufzeit',
  facet_access: 'Zugriff',
  facet_protection: 'Schutz',

  fstate_satisfied: 'Erfüllt',
  fstate_pending: 'Ausstehend',
  fstate_progressing: 'In Bearbeitung',
  fstate_degraded: 'Beeinträchtigt',
  fstate_failed: 'Fehlgeschlagen',
  fstate_blocked: 'Blockiert',
  fstate_unknown: 'Unbekannt',
  fstate_not_applicable: 'Nicht zutreffend',

  reasonUnknown: 'Unbekannter Zustand',
  healthy: 'Gesund',
  'awaiting-delivery': 'Wartet auf Auslieferung',
  'configuration-evidence-unknown': 'Konfigurationsnachweis nicht verfügbar',
  'capability-disabled': 'Nicht ausgewählt',
  'dependency-unmet': 'Eine erforderliche Abhängigkeit ist nicht bereit',
  'required-values-missing': 'Erforderliche Konfigurationswerte fehlen',
  'not-declared-in-git': 'Noch nicht im GitOps-Overlay deklariert',
  'delivery-evidence-unknown': 'Argo-CD-Nachweis nicht verfügbar',
  'delivery-failed': 'Argo CD meldet die Anwendung als beeinträchtigt',
  'delivery-progressing': 'Argo CD gleicht ab',
  'delivery-pending': 'Argo CD hat die Anwendung noch nicht erstellt',
  'delivery-drifted': 'Die laufende Anwendung weicht von Git ab',
  'delivery-out-of-sync': 'Die Anwendung ist nicht synchron',
  'runtime-evidence-unknown': 'Laufzeitnachweis nicht verfügbar',
  'runtime-jobs-failed': 'Ein oder mehrere Jobs sind fehlgeschlagen',
  'runtime-pvc-unbound': 'Ein Persistent Volume Claim ist ungebunden',
  'runtime-workloads-not-ready': 'Workloads sind nicht bereit',
  'runtime-probes-failing': 'Anwendungs-Probes schlagen fehl',
  'access-evidence-unknown': 'Zugriffsnachweis nicht verfügbar',
  'access-dns-unresolved': 'DNS wird nicht aufgelöst',
  'access-certificate-not-ready': 'Das TLS-Zertifikat ist nicht bereit',
  'access-public-unreachable': 'Über öffentlichen Ingress nicht erreichbar',
  'access-gateway-unreachable': 'Über das Private Gateway nicht erreichbar',
  'access-private-exposed-publicly': 'Eine private Fähigkeit ist aus dem öffentlichen Internet erreichbar',
  'access-internal-exposed': 'Eine interne Fähigkeit ist über Ingress erreichbar',
  'protection-not-applicable': 'Keine zu schützenden Datensätze',
  'protection-evidence-unknown': 'Schutznachweis nicht verfügbar',
  'protection-coverage-gap': 'Einige Datensätze sind nicht durch Backups abgedeckt',
  'protection-no-local-recovery-point': 'Es existiert kein lokaler Wiederherstellungspunkt',
  'protection-local-recovery-point-stale': 'Der lokale Wiederherstellungspunkt ist veraltet',
  'protection-no-offsite-recovery-point': 'Es existiert kein externer Wiederherstellungspunkt',
  'protection-offsite-recovery-point-stale': 'Der externe Wiederherstellungspunkt ist veraltet',
  'protection-retention-unmet': 'Aufbewahrungsrichtlinie nicht erfüllt'
};

const messages: Record<Locale, Record<ConsoleMessageKey, string>> = { en, de };

export function consoleTranslate(locale: Locale, key: ConsoleMessageKey): string {
  return messages[locale][key];
}

/** reasonKey resolves a backend reason code to a message key, falling back to a
 *  generic label for any code this build does not yet localize. */
export function reasonKey(code: string): ConsoleMessageKey {
  return (Object.prototype.hasOwnProperty.call(en, code) ? code : 'reasonUnknown') as ConsoleMessageKey;
}

export function stateKey(state: CapabilityState): ConsoleMessageKey {
  return `state_${state}` as ConsoleMessageKey;
}

export function facetKindKey(kind: FacetKind): ConsoleMessageKey {
  return `facet_${kind}` as ConsoleMessageKey;
}

export function facetStateKey(state: FacetState): ConsoleMessageKey {
  return `fstate_${state.replace(/-/g, '_')}` as ConsoleMessageKey;
}

export function roleKey(role: ConsoleRole | undefined): ConsoleMessageKey {
  return `role_${role ? role : 'none'}` as ConsoleMessageKey;
}

export function protectionLevelKey(level: ProtectionLevel): ConsoleMessageKey {
  return `plevel_${level}` as ConsoleMessageKey;
}

export function stepKey(kind: string): ConsoleMessageKey {
  return `step_${kind}` as ConsoleMessageKey;
}

export function lockoutKey(reason: string): ConsoleMessageKey {
  return `lockout_${reason}` as ConsoleMessageKey;
}

export function dataTypeKey(dataType: DataType): ConsoleMessageKey {
  return `dtype_${dataType}` as ConsoleMessageKey;
}

/** catalogLabelKey resolves a catalog exposure/protection slug to a message key,
 *  or null when this build has no friendly label for it (the caller then shows
 *  the raw slug rather than inventing a translation). */
export function catalogLabelKey(value: string): ConsoleMessageKey | null {
  const key = `cat_${value}`;
  return Object.prototype.hasOwnProperty.call(en, key) ? (key as ConsoleMessageKey) : null;
}
