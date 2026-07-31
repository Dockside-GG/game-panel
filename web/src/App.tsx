import { useState } from "react";
import {
  Activity,
  ArrowRight,
  Boxes,
  Check,
  ChevronRight,
  CircleGauge,
  Clock3,
  Copy,
  Cpu,
  Download,
  ExternalLink,
  FileWarning,
  Eye,
  EyeOff,
  HardDrive,
  Info,
  KeyRound,
  MemoryStick,
  PackageCheck,
  Plus,
  RotateCw,
  Server,
  ShieldCheck,
  UserPlus,
  Users,
  X,
} from "lucide-react";
import {
  Link,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useParams,
} from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api, beginDiscord } from "./api";
import {
  AppShell,
  Brand,
  EmptyState,
  ErrorPanel,
  LoadingScreen,
  PageHeader,
  StatusBadge,
  UserAvatar,
} from "./components";
import type {
  BuildInfo,
  Dashboard,
  Invite,
  Session,
  SetupStatus,
  ServerSummary,
  SystemContainer,
  SystemContainerLogs,
  DiagnosticEntry,
  PanelUpdate,
  User,
} from "./types";
import { serverPresentation } from "./server-presentation";
import {
  NewServerPage,
  ServerDetailPage,
  ServersPage,
  TemplateDetailPage,
  TemplateEditorPage,
  TemplateLibraryPage,
} from "./fleet-pages";

function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/v1/session"),
    retry: false,
  });
}

function ProtectedLayout() {
  const session = useSession();
  const location = useLocation();
  if (session.isLoading) return <LoadingScreen />;
  if (session.error instanceof ApiError && session.error.status === 401) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }
  if (session.isError || !session.data) {
    return (
      <div className="center-screen">
        <ErrorPanel error={session.error} retry={() => void session.refetch()} />
      </div>
    );
  }
  if (session.data.user.status === "pending") {
    return <Navigate to="/pending" replace />;
  }
  return <AppShell session={session.data} />;
}

function LoginPage() {
  const [claimToken, setClaimToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const status = useQuery({
    queryKey: ["setup-status"],
    queryFn: () => api<SetupStatus>("/api/v1/setup/status"),
    retry: false,
  });
  const location = useLocation();
  const errorCode = new URLSearchParams(location.search).get("error");
  const messages: Record<string, string> = {
    mfa_required:
      "This panel requires Discord MFA for your assigned role. Enable MFA in Discord and try again.",
    invite_invalid: "That invite is invalid, expired, or has already been used.",
    already_claimed: "This installation has already been claimed.",
    access_denied: "Your Discord account does not have access to this panel.",
    oauth_state: "The sign-in request expired or could not be verified. Please try again.",
    discord_exchange: "Discord could not complete sign-in. Please try again.",
  };

  const submit = async (purpose: "login" | "claim") => {
    setBusy(true);
    setError(undefined);
    try {
      await beginDiscord({
        purpose,
        ...(purpose === "claim" ? { claim_token: claimToken } : {}),
      });
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Unable to begin sign-in.");
      setBusy(false);
    }
  };

  return (
    <div className="auth-layout">
      <section className="auth-story">
        <Brand />
        <div className="story-copy">
          <span className="eyebrow">YOUR SERVERS. YOUR HARDWARE.</span>
          <h1>Game hosting without the operational drag.</h1>
          <p>
            Provision, monitor, and manage every community server from one private,
            container-native control plane.
          </p>
          <div className="feature-row">
            <span><ShieldCheck size={18} /> Discord identity</span>
            <span><CircleGauge size={18} /> Live telemetry</span>
            <span><Boxes size={18} /> Docker isolation</span>
          </div>
        </div>
        <small>Dockside Game Panel · Self-hosted by design</small>
      </section>
      <main className="auth-main">
        <div className="mobile-auth-brand"><Brand /></div>
        <div className="auth-card">
          {status.isLoading ? (
            <span className="loader" />
          ) : status.isError ? (
            <ErrorPanel error={status.error} retry={() => void status.refetch()} />
          ) : !status.data?.claimed ? (
            <>
              <span className="auth-icon"><KeyRound size={25} /></span>
              <span className="eyebrow">FIRST-RUN SETUP</span>
              <h2>Claim this panel</h2>
              <p>
                Enter the one-time bootstrap token shown by the installer, then
                verify the owner account with Discord.
              </p>
              <label>
                Bootstrap token
                <input
                  type="password"
                  value={claimToken}
                  onChange={(event) => setClaimToken(event.target.value)}
                  autoComplete="one-time-code"
                  placeholder="Paste the installer token"
                />
              </label>
              {(error || errorCode) && (
                <div className="form-error">
                  {error || messages[errorCode || ""] || "Authentication failed."}
                </div>
              )}
              <button
                className="button primary wide"
                type="button"
                disabled={busy || claimToken.length < 16}
                onClick={() => void submit("claim")}
              >
                Continue with Discord <ArrowRight size={18} />
              </button>
              <p className="fine-print">
                The token is consumed after a successful claim. Dockside only asks
                Discord for your basic identity.
              </p>
            </>
          ) : (
            <>
              <span className="auth-icon discord"><ShieldCheck size={25} /></span>
              <span className="eyebrow">SECURE ACCESS</span>
              <h2>Welcome aboard</h2>
              <p>
                Sign in with the Discord account approved for this private panel.
              </p>
              {(error || errorCode) && (
                <div className="form-error">
                  {error || messages[errorCode || ""] || "Authentication failed."}
                </div>
              )}
              <button
                className="button discord wide"
                type="button"
                disabled={busy}
                onClick={() => void submit("login")}
              >
                <DiscordGlyph /> Continue with Discord
              </button>
              <div className="auth-note">
                <ShieldCheck size={17} />
                Access is invite-only. Your owner controls panel permissions and
                may require Discord MFA.
              </div>
            </>
          )}
        </div>
      </main>
    </div>
  );
}

function DiscordGlyph() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="currentColor"
        d="M19.54 5.34A16.3 16.3 0 0 0 15.44 4l-.5 1.02a15.1 15.1 0 0 0-5.87 0L8.56 4a16.5 16.5 0 0 0-4.1 1.35C1.86 9.2 1.16 12.96 1.5 16.66a16.7 16.7 0 0 0 5.03 2.53l1.22-1.66c-.67-.25-1.3-.57-1.9-.96l.47-.37c3.67 1.7 7.65 1.7 11.27 0l.48.37c-.6.4-1.24.72-1.9.97l1.22 1.65a16.6 16.6 0 0 0 5.02-2.53c.4-4.3-.68-8.02-2.87-11.32ZM8.52 14.4c-1.1 0-2.01-1.01-2.01-2.25s.89-2.25 2.01-2.25c1.13 0 2.03 1.02 2.01 2.25 0 1.24-.89 2.25-2.01 2.25Zm6.97 0c-1.1 0-2.01-1.01-2.01-2.25s.89-2.25 2.01-2.25c1.13 0 2.03 1.02 2.01 2.25 0 1.24-.88 2.25-2.01 2.25Z"
      />
    </svg>
  );
}

function InvitePage() {
  const { token = "" } = useParams();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  return (
    <div className="center-screen textured">
      <div className="auth-card invite-card">
        <Brand />
        <span className="auth-icon"><UserPlus size={25} /></span>
        <span className="eyebrow">PRIVATE INVITATION</span>
        <h2>Join this Dockside panel</h2>
        <p>
          Verify your Discord identity to claim this single-use invite. The panel
          owner will review your account before granting access.
        </p>
        {error && <div className="form-error">{error}</div>}
        <button
          className="button discord wide"
          type="button"
          disabled={busy || token.length < 16}
          onClick={async () => {
            setBusy(true);
            setError(undefined);
            try {
              await beginDiscord({ purpose: "invite", invite_token: token });
            } catch (caught) {
              setError(
                caught instanceof Error ? caught.message : "Unable to claim invite.",
              );
              setBusy(false);
            }
          }}
        >
          <DiscordGlyph /> Verify with Discord
        </button>
        <p className="fine-print">
          This does not add a bot to your Discord server and the panel will not DM
          you.
        </p>
      </div>
    </div>
  );
}

function PendingPage() {
  const session = useSession();
  const navigate = useNavigate();
  if (session.isLoading) return <LoadingScreen />;
  if (session.error instanceof ApiError && session.error.status === 401) {
    return <Navigate to="/login" replace />;
  }
  if (session.data?.user.status === "active") {
    return <Navigate to="/dashboard" replace />;
  }
  return (
    <div className="center-screen textured">
      <div className="pending-card">
        <span className="pending-mark"><Clock3 size={30} /></span>
        <span className="eyebrow">IDENTITY VERIFIED</span>
        <h1>Waiting for owner approval</h1>
        <p>
          Your Discord account is connected, but no panel or game server data is
          visible yet. The owner must review your identity and assign your access.
        </p>
        {session.data && (
          <div className="identity-preview">
            <UserAvatar user={session.data.user} size={46} />
            <div>
              <strong>{session.data.user.global_name || session.data.user.username}</strong>
              <span>@{session.data.user.username}</span>
            </div>
            <StatusBadge tone="warning">Pending</StatusBadge>
          </div>
        )}
        <div className="pending-actions">
          <button
            className="button secondary"
            type="button"
            onClick={() => void session.refetch()}
          >
            Check approval
          </button>
          <button
            className="button ghost"
            type="button"
            onClick={async () => {
              await api<void>("/api/v1/auth/logout", { method: "POST" });
              await navigate("/login", { replace: true });
            }}
          >
            Sign out
          </button>
        </div>
      </div>
    </div>
  );
}

function DashboardPage() {
  const dashboard = useQuery({
    queryKey: ["dashboard"],
    queryFn: () => api<Dashboard>("/api/v1/dashboard"),
    refetchInterval: 15_000,
  });
  const fleet = useQuery({
    queryKey: ["servers", "dashboard-health"],
    queryFn: () => api<{ servers: ServerSummary[] }>("/api/v1/servers"),
    refetchInterval: 5_000,
  });
  const restartWorker = useMutation({
    mutationFn: () => api<void>("/api/v1/system/containers/worker/restart", {
      method: "POST",
    }),
    onSuccess: () => void dashboard.refetch(),
  });
  const navigate = useNavigate();
  if (dashboard.isLoading) return <PageLoading title="Dashboard" />;
  if (dashboard.isError || !dashboard.data) {
    return <ErrorPanel error={dashboard.error} retry={() => void dashboard.refetch()} />;
  }
  const { servers, host, recent_activity: activityItems } = dashboard.data;
  const memoryPercent = host.memory_bytes
    ? ((host.memory_used_bytes ?? 0) / host.memory_bytes) * 100
    : 0;
  const dataDiskPercent = host.data_filesystem?.total_bytes
    ? (host.data_filesystem.used_bytes / host.data_filesystem.total_bytes) * 100
    : 0;
  const backupDiskPercent = host.backup_filesystem?.total_bytes
    ? (host.backup_filesystem.used_bytes / host.backup_filesystem.total_bytes) * 100
    : 0;
  const telemetryStale = host.observed_at
    ? dashboard.dataUpdatedAt - new Date(host.observed_at).getTime() > 30_000
    : true;
  return (
    <>
      <PageHeader
        eyebrow="COMMAND CENTER"
        title="Dashboard"
        description="A live overview of this host and every managed game server."
        actions={
          <button className="button primary" onClick={() => void navigate("/servers/new")}>
            <Plus size={18} /> New server
          </button>
        }
      />
      {dashboard.data.host_error && (
        <div className="notice warning">{dashboard.data.host_error}</div>
      )}
      {fleet.isError && (
        <div className="notice warning">Live game-server telemetry is temporarily unavailable.</div>
      )}
      <section className="metric-grid server-metrics">
        <MetricCard label="Game servers" value={servers.total} icon={Server} accent="blue" />
        <MetricCard label="Running" value={servers.running} icon={Activity} accent="green" />
        <MetricCard label="Stopped" value={servers.stopped} icon={Boxes} accent="red" />
        <MetricCard
          label="Needs attention"
          value={servers.attention}
          icon={ShieldCheck}
          accent="amber"
        />
      </section>
      <FleetHealthPanel
        servers={fleet.data?.servers ?? []}
        loading={fleet.isLoading}
      />
      <div className="dashboard-columns">
        <section className="panel">
          <div className="panel-heading">
            <div>
              <span className="eyebrow">HOST</span>
              <h2>Docker engine</h2>
            </div>
            <StatusBadge tone={dashboard.data.host_error || telemetryStale ? "warning" : "success"}>
              {dashboard.data.host_error ? "Unavailable" : telemetryStale ? "Telemetry stale" : "Connected"}
            </StatusBadge>
          </div>
          <div className="host-grid">
            <HostItem icon={Cpu} label="CPU capacity" value={host.cpus ? `${host.cpus} cores` : "—"} />
            <HostItem icon={MemoryStick} label="Memory capacity" value={formatBytes(host.memory_bytes)} />
            <HostItem icon={Boxes} label="Containers" value={host.containers ?? "—"} />
            <HostItem icon={HardDrive} label="Docker version" value={host.engine_version || "—"} />
          </div>
          <div className="host-usage-grid">
            <UsageMeter
              label="CPU usage"
              value={host.cpu_usage_percent ?? 0}
              detail={host.telemetry_available ? formatPercent(host.cpu_usage_percent) : "Collecting telemetry…"}
              available={host.telemetry_available}
            />
            <UsageMeter
              label="Memory usage"
              value={memoryPercent}
              detail={host.telemetry_available
                ? `${formatBytes(host.memory_used_bytes)} / ${formatBytes(host.memory_bytes)}`
                : "Collecting telemetry…"}
              available={host.telemetry_available}
            />
            <UsageMeter
              label="Game data disk"
              value={dataDiskPercent}
              detail={host.data_filesystem
                ? `${formatBytes(host.data_filesystem.used_bytes)} / ${formatBytes(host.data_filesystem.total_bytes)}`
                : "Unavailable"}
              available={Boolean(host.data_filesystem)}
            />
            <UsageMeter
              label="Backup disk"
              value={backupDiskPercent}
              detail={host.backup_filesystem
                ? `${formatBytes(host.backup_filesystem.used_bytes)} / ${formatBytes(host.backup_filesystem.total_bytes)}`
                : "Unavailable"}
              available={Boolean(host.backup_filesystem)}
            />
          </div>
          <dl className="host-details">
            <div><dt>Engine instance</dt><dd>{host.instance_id || "Not reported"}</dd></div>
            <div><dt>Telemetry scope</dt><dd>Docker host / Linux VM</dd></div>
            <div><dt>Load average</dt><dd>{host.telemetry_available ? `${host.load_1?.toFixed(2)} / ${host.load_5?.toFixed(2)} / ${host.load_15?.toFixed(2)}` : "Collecting"}</dd></div>
            <div><dt>Last sample</dt><dd>{host.observed_at ? formatDate(host.observed_at) : "Not reported"}</dd></div>
            <div><dt>Platform</dt><dd>{host.operating_system || "Not reported"}</dd></div>
            <div><dt>Architecture</dt><dd>{host.architecture || "Not reported"}</dd></div>
          </dl>
        </section>
        <section className="panel activity-panel">
          <div className="panel-heading">
            <div>
              <span className="eyebrow">AUDIT TRAIL</span>
              <h2>Recent activity</h2>
            </div>
          </div>
          {activityItems.length === 0 ? (
            <EmptyState
              icon={Activity}
              title="No activity yet"
              description="Server actions, schedules, and important events will appear here."
            />
          ) : (
            <div className="activity-list">
              {activityItems.slice(0, 6).map((item) => (
                <div className="activity-row" key={item.id}>
                  <span className={`activity-dot ${item.severity}`} />
                  <div>
                    <strong>{item.summary}</strong>
                    <span>{formatDate(item.created_at)}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
      {(dashboard.data.system_containers || dashboard.data.system_containers_error) && (
        <SystemContainersPanel
          containers={dashboard.data.system_containers ?? []}
          error={dashboard.data.system_containers_error}
          canRestartWorker={Boolean(dashboard.data.can_restart_worker)}
          restarting={restartWorker.isPending}
          restartError={restartWorker.isError ? restartWorker.error.message : undefined}
          onRestartWorker={() => {
            if (window.confirm("Restart the Dockside background worker? Running jobs may be retried.")) {
              restartWorker.mutate();
            }
          }}
        />
      )}
    </>
  );
}

function FleetHealthPanel({ servers, loading }: { servers: ServerSummary[]; loading: boolean }) {
  return <section className="panel dashboard-fleet-health">
    <div className="panel-heading">
      <div><span className="eyebrow">FLEET HEALTH</span><h2>Live game-server resources</h2></div>
      <StatusBadge tone={servers.some((server) => serverPresentation(server).warning) ? "warning" : "success"}>{servers.length} visible</StatusBadge>
    </div>
    {loading ? <TableLoading /> : servers.length ? <div className="dashboard-fleet-table-wrap"><table className="dashboard-fleet-table">
      <thead><tr><th>Server</th><th>Status</th><th>CPU</th><th>Memory</th><th>Disk</th><th>I/O</th><th><span className="sr-only">Open</span></th></tr></thead>
      <tbody>
      {servers.map((server) => {
        const cpuCapacity = server.resources.cpu_limit_millicores ? server.resources.cpu_limit_millicores / 10 : Math.max(server.runtime.cpu_percent ?? 0, 100);
        const cpu = cpuCapacity ? ((server.runtime.cpu_percent ?? 0) / cpuCapacity) * 100 : 0;
        const memoryCapacity = server.resources.memory_limit_bytes ?? server.runtime.memory_limit_bytes;
        const memory = memoryCapacity ? ((server.runtime.memory_bytes ?? 0) / memoryCapacity) * 100 : 0;
        const disk = server.resources.disk_limit_bytes ? ((server.runtime.disk_bytes ?? 0) / server.resources.disk_limit_bytes) * 100 : 0;
        return <tr key={server.id}>
          <td data-label="Server"><Link className="dashboard-server-link" to={`/servers/${server.id}`}><strong>{server.name}</strong><span>{server.template_name}</span></Link></td>
          <td data-label="Status"><ServerStatus server={server} /></td>
          <td data-label="CPU"><UsageMeter label="CPU" value={cpu} detail={`${formatPercent(server.runtime.cpu_percent)} current`} /></td>
          <td data-label="Memory"><UsageMeter label="Memory" value={memory} detail={`${formatBytes(server.runtime.memory_bytes)} / ${memoryCapacity ? formatBytes(memoryCapacity) : "unlimited"}`} available={Boolean(memoryCapacity)} /></td>
          <td data-label="Disk"><UsageMeter label="Disk" value={disk} detail={`${formatBytes(server.runtime.disk_bytes)}${server.resources.disk_limit_bytes ? ` / ${formatBytes(server.resources.disk_limit_bytes)}` : " · no alert limit"}`} available={Boolean(server.resources.disk_limit_bytes)} /></td>
          <td data-label="I/O"><small className="dashboard-server-io">Network {formatBytes((server.runtime.network_rx_bytes ?? 0) + (server.runtime.network_tx_bytes ?? 0))}<br />Block {formatBytes((server.runtime.block_read_bytes ?? 0) + (server.runtime.block_write_bytes ?? 0))}</small></td>
          <td className="dashboard-server-open"><Link className="icon-button" to={`/servers/${server.id}`} aria-label={`Open ${server.name}`}><ChevronRight size={15} /></Link></td>
        </tr>;
      })}
      </tbody>
    </table></div> : <EmptyState icon={Server} title="No live server telemetry yet" description="Provision a game server to see CPU, memory, disk, network, and block-I/O health here." />}
  </section>;
}

function UsersPage() {
  const [showInvite, setShowInvite] = useState(false);
  const users = useQuery({
    queryKey: ["users"],
    queryFn: () => api<{ users: User[] }>("/api/v1/users"),
  });
  const invites = useQuery({
    queryKey: ["invites"],
    queryFn: () => api<{ invites: Invite[] }>("/api/v1/invites"),
  });
  const servers = useQuery({
    queryKey: ["servers", "access-control"],
    queryFn: () => api<{ servers: ServerSummary[] }>("/api/v1/servers"),
  });
  const installation = useQuery({
    queryKey: ["installation-settings"],
    queryFn: () => api<{
      public_url: string;
      discord_client_id: string;
      discord_secret_configured: boolean;
      mfa_policy: string;
    }>("/api/v1/installation/settings"),
  });
  return (
    <>
      <PageHeader
        eyebrow="ACCESS CONTROL"
        title="Users & access"
        description="Review Discord identities before granting panel or server permissions."
        actions={
          <button className="button primary" onClick={() => setShowInvite(true)}>
            <UserPlus size={18} /> Create invite
          </button>
        }
      />
      {(users.isError || invites.isError || servers.isError || installation.isError) && (
        <ErrorPanel
          error={users.error || invites.error || servers.error || installation.error}
          retry={() => {
            void users.refetch();
            void invites.refetch();
            void servers.refetch();
            void installation.refetch();
          }}
        />
      )}
      {installation.data && <MFASettings policy={installation.data.mfa_policy} />}
      <section className="panel data-panel">
        <div className="panel-heading">
          <div><span className="eyebrow">PEOPLE</span><h2>Panel users</h2></div>
          <StatusBadge>{users.data?.users.length ?? 0} total</StatusBadge>
        </div>
        {users.isLoading ? (
          <TableLoading />
        ) : users.data?.users.length ? (
          <UsersTable users={users.data.users} servers={servers.data?.servers ?? []} />
        ) : (
          <EmptyState icon={Users} title="No users found" description="Create an invite to add someone." />
        )}
      </section>
      <section className="panel data-panel">
        <div className="panel-heading">
          <div><span className="eyebrow">SINGLE USE</span><h2>Invitations</h2></div>
        </div>
        {invites.isLoading ? (
          <TableLoading />
        ) : invites.data?.invites.length ? (
          <InvitesTable invites={invites.data.invites} />
        ) : (
          <EmptyState
            icon={KeyRound}
            title="No invitation links"
            description="Each invite expires and can be claimed by one Discord account."
          />
        )}
      </section>
      {showInvite && <InviteDialog onClose={() => setShowInvite(false)} />}
    </>
  );
}

function MFASettings({ policy }: { policy: string }) {
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState(policy);
  const save = useMutation({
    mutationFn: () =>
      api<void>("/api/v1/installation/settings", {
        method: "PUT",
        body: JSON.stringify({ mfa_policy: selected }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["installation-settings"] }),
  });
  return (
    <section className="panel mfa-settings">
      <div><span className="mfa-setting-mark"><ShieldCheck size={18} /></span><div><span className="eyebrow">DISCORD MFA POLICY</span><h2>Require MFA by responsibility</h2><p>Discord reports whether the account has MFA enabled at every sign-in.</p></div></div>
      <select value={selected} onChange={(event) => setSelected(event.target.value)}>
        <option value="administrators">Owners & administrators</option>
        <option value="operators">Administrators & operators</option>
        <option value="everyone">Every panel user</option>
        <option value="off">Do not require Discord MFA</option>
      </select>
      <button className="button primary" disabled={save.isPending || selected === policy} onClick={() => save.mutate()}>{save.isPending ? "Saving..." : "Save MFA policy"}</button>
      {save.isError && <div className="form-error">{save.error.message}</div>}
    </section>
  );
}

function UsersTable({ users, servers }: { users: User[]; servers: ServerSummary[] }) {
  const queryClient = useQueryClient();
  const [accessUser, setAccessUser] = useState<User | null>(null);
  const mutation = useMutation({
    mutationFn: ({ id, action, role }: { id: string; action: "activate" | "reject"; role?: string }) =>
      api<void>(`/api/v1/users/${id}/${action}`, {
        method: "POST",
        body: action === "activate" ? JSON.stringify({ panel_role: role }) : undefined,
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["users"] }),
  });
  const update = useMutation({
    mutationFn: ({ user, role, status }: { user: User; role: User["panel_role"]; status: "active" | "suspended" }) =>
      api<void>(`/api/v1/users/${user.id}`, {
        method: "PATCH",
        body: JSON.stringify({ panel_role: role, status }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["users"] }),
  });
  return (
    <>
    <div className="table-wrap">
      <table>
        <thead><tr><th>User</th><th>MFA</th><th>Status</th><th>Role</th><th><span className="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {users.map((user) => (
            <tr key={user.id}>
              <td>
                <div className="table-user">
                  <UserAvatar user={user} />
                  <div><strong>{user.global_name || user.username}</strong><span>@{user.username}</span></div>
                </div>
              </td>
              <td>{user.mfa_enabled ? <StatusBadge tone="success">Enabled</StatusBadge> : <StatusBadge>Off</StatusBadge>}</td>
              <td><StatusBadge tone={user.status === "active" ? "success" : user.status === "pending" ? "warning" : "neutral"}>{user.status}</StatusBadge></td>
              <td className="capitalize">
                {user.panel_role === "owner" || user.status === "pending" ? user.panel_role : (
                  <select
                    value={user.panel_role}
                    disabled={update.isPending}
                    onChange={(event) => update.mutate({
                      user,
                      role: event.target.value as User["panel_role"],
                      status: user.status === "suspended" ? "suspended" : "active",
                    })}
                  >
                    <option value="viewer">Viewer</option>
                    <option value="operator">Operator</option>
                    <option value="administrator">Administrator</option>
                  </select>
                )}
              </td>
              <td className="row-actions">
                {user.status === "pending" && (
                  <>
                    <button
                      className="button small secondary"
                      disabled={mutation.isPending}
                      onClick={() => mutation.mutate({ id: user.id, action: "activate", role: "viewer" })}
                    ><Check size={15} /> Approve</button>
                    <button
                      className="icon-button danger"
                      aria-label={`Reject ${user.username}`}
                      disabled={mutation.isPending}
                      onClick={() => mutation.mutate({ id: user.id, action: "reject" })}
                    ><X size={16} /></button>
                  </>
                )}
                {user.status !== "pending" && user.panel_role !== "owner" && (
                  <>
                    <button className="button small secondary" onClick={() => setAccessUser(user)}><KeyRound size={14} /> Server access</button>
                    <button
                      className="button small ghost"
                      disabled={update.isPending}
                      onClick={() => update.mutate({
                        user,
                        role: user.panel_role,
                        status: user.status === "suspended" ? "active" : "suspended",
                      })}
                    >{user.status === "suspended" ? "Reactivate" : "Suspend"}</button>
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {(mutation.isError || update.isError) && <div className="form-error table-error">{(mutation.error || update.error)?.message}</div>}
    </div>
    {accessUser && <ServerAccessDialog user={accessUser} servers={servers} onClose={() => setAccessUser(null)} />}
    </>
  );
}

type ServerAccessBinding = {
  server_id: string;
  server_name: string;
  role: "administrator" | "operator" | "viewer";
};

function ServerAccessDialog({ user, servers, onClose }: { user: User; servers: ServerSummary[]; onClose: () => void }) {
  const access = useQuery({
    queryKey: ["user-server-access", user.id],
    queryFn: () => api<{ bindings: ServerAccessBinding[] }>(`/api/v1/users/${user.id}/server-access`),
  });
  if (access.isLoading) {
    return <div className="dialog-backdrop"><div className="dialog"><div className="wizard-loading"><span className="loader" /></div></div></div>;
  }
  if (access.isError || !access.data) {
    return <div className="dialog-backdrop"><div className="dialog"><ErrorPanel error={access.error} retry={() => void access.refetch()} /><div className="dialog-actions"><button className="secondary-button" onClick={onClose}>Close</button></div></div></div>;
  }
  return <ServerAccessEditor key={JSON.stringify(access.data.bindings)} user={user} servers={servers} initial={access.data.bindings} onClose={onClose} />;
}

function ServerAccessEditor({ user, servers, initial, onClose }: { user: User; servers: ServerSummary[]; initial: ServerAccessBinding[]; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [roles, setRoles] = useState<Record<string, string>>(
    Object.fromEntries(initial.map((binding) => [binding.server_id, binding.role])),
  );
  const allowedRoles = user.panel_role === "administrator"
    ? ["administrator", "operator", "viewer"]
    : user.panel_role === "operator"
      ? ["operator", "viewer"]
      : ["viewer"];
  const save = useMutation({
    mutationFn: () =>
      api<void>(`/api/v1/users/${user.id}/server-access`, {
        method: "PUT",
        body: JSON.stringify({
          bindings: Object.entries(roles)
            .filter(([, role]) => role)
            .map(([server_id, role]) => ({ server_id, role })),
        }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["user-server-access", user.id] });
      onClose();
    },
  });
  return (
    <div className="dialog-backdrop">
      <div className="dialog access-dialog" role="dialog" aria-modal="true">
        <h2>Server access for {user.global_name || user.username}</h2>
        <p>The panel role is the maximum capability. Select exactly which servers this user can see and operate.</p>
        {user.panel_role === "administrator" && <div className="notice warning"><ShieldCheck size={15} /> Administrators currently have installation-wide server access; these bindings document intended scope.</div>}
        <div className="access-server-list">
          {servers.map((server) => {
            const role = roles[server.id] ?? "";
            return (
              <article key={server.id}>
                <label><input type="checkbox" checked={role !== ""} onChange={(event) => setRoles((current) => ({ ...current, [server.id]: event.target.checked ? allowedRoles[0]! : "" }))} /><span><strong>{server.name}</strong><small>{server.template_name}</small></span></label>
                <select value={role} disabled={!role} onChange={(event) => setRoles((current) => ({ ...current, [server.id]: event.target.value }))}>
                  {allowedRoles.map((item) => <option key={item} value={item}>{item}</option>)}
                </select>
              </article>
            );
          })}
          {servers.length === 0 && <EmptyState icon={Server} title="No servers" description="Create a server before assigning scoped access." />}
        </div>
        {save.isError && <div className="form-error">{save.error.message}</div>}
        <div className="dialog-actions"><button className="secondary-button" onClick={onClose}>Cancel</button><button className="primary-button" disabled={save.isPending} onClick={() => save.mutate()}>{save.isPending ? "Saving..." : "Save server access"}</button></div>
      </div>
    </div>
  );
}

function InvitesTable({ invites }: { invites: Invite[] }) {
  const [renderedAt] = useState(Date.now);
  const queryClient = useQueryClient();
  const revoke = useMutation({
    mutationFn: (id: string) => api<void>(`/api/v1/invites/${id}`, { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["invites"] }),
  });
  return (
    <div className="table-wrap">
      <table>
        <thead><tr><th>Label</th><th>Created</th><th>Expires</th><th>Status</th><th><span className="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {invites.map((invite) => {
            const expired = new Date(invite.expires_at).getTime() < renderedAt;
            const state = invite.revoked_at ? "Revoked" : invite.claimed_at ? "Claimed" : expired ? "Expired" : "Available";
            return (
              <tr key={invite.id}>
                <td><strong>{invite.label || "Untitled invite"}</strong></td>
                <td>{formatDate(invite.created_at)}</td>
                <td>{formatDate(invite.expires_at)}</td>
                <td><StatusBadge tone={state === "Available" ? "success" : state === "Claimed" ? "info" : "neutral"}>{state}</StatusBadge></td>
                <td className="row-actions">
                  {state === "Available" && (
                    <button className="button small ghost" disabled={revoke.isPending} onClick={() => revoke.mutate(invite.id)}>Revoke</button>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function InviteDialog({ onClose }: { onClose: () => void }) {
  const [label, setLabel] = useState("");
  const [hours, setHours] = useState(24);
  const [inviteURL, setInviteURL] = useState("");
  const [copied, setCopied] = useState(false);
  const queryClient = useQueryClient();
  const create = useMutation({
    mutationFn: () =>
      api<{ invite: Invite; invite_url: string }>("/api/v1/invites", {
        method: "POST",
        body: JSON.stringify({ label, expires_in_hours: hours }),
      }),
    onSuccess: (data) => {
      setInviteURL(data.invite_url);
      void queryClient.invalidateQueries({ queryKey: ["invites"] });
    },
  });
  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="invite-title" onMouseDown={(event) => event.stopPropagation()}>
        <button className="icon-button dialog-close" onClick={onClose} aria-label="Close"><X size={18} /></button>
        <span className="auth-icon"><UserPlus size={23} /></span>
        <h2 id="invite-title">{inviteURL ? "Invite ready" : "Create single-use invite"}</h2>
        {!inviteURL ? (
          <>
            <p>The claimant stays pending with no panel visibility until you approve them.</p>
            <label>Label <input value={label} maxLength={120} placeholder="Alex — weekend moderators" onChange={(event) => setLabel(event.target.value)} /></label>
            <label>Expires after
              <select value={hours} onChange={(event) => setHours(Number(event.target.value))}>
                <option value={1}>1 hour</option>
                <option value={24}>24 hours</option>
                <option value={72}>3 days</option>
                <option value={168}>7 days</option>
              </select>
            </label>
            {create.isError && <div className="form-error">{create.error.message}</div>}
            <button className="button primary wide" disabled={create.isPending} onClick={() => create.mutate()}>
              <KeyRound size={17} /> Generate link
            </button>
          </>
        ) : (
          <>
            <p>Send this link through a trusted channel. It is only shown in full now.</p>
            <div className="copy-field"><input readOnly value={inviteURL} /><button className="icon-button" onClick={async () => { await navigator.clipboard.writeText(inviteURL); setCopied(true); }} aria-label="Copy invite"><Copy size={17} /></button></div>
            {copied && <div className="copy-confirm"><Check size={15} /> Copied to clipboard</div>}
            <button className="button secondary wide" onClick={onClose}>Done</button>
          </>
        )}
      </div>
    </div>
  );
}

function ActivityPage() {
  const dashboard = useQuery({
    queryKey: ["dashboard", "activity-page"],
    queryFn: () => api<Dashboard>("/api/v1/dashboard"),
    refetchInterval: 10_000,
  });
  return (
    <>
      <PageHeader eyebrow="AUDIT TRAIL" title="Activity" description="Review the latest server events, schedules, access changes, and operations visible to your account." />
      {dashboard.isError ? (
        <ErrorPanel error={dashboard.error} retry={() => void dashboard.refetch()} />
      ) : dashboard.isLoading ? (
        <section className="panel"><TableLoading /></section>
      ) : dashboard.data?.recent_activity.length ? (
        <section className="panel global-activity-list">
          {dashboard.data.recent_activity.map((item) => (
            <article key={item.id}>
              <span className={`activity-dot ${item.severity}`} />
              <div>
                <strong>{item.summary}</strong>
                <span>{item.event_type} · {formatDate(item.created_at)}</span>
              </div>
              {item.server_id && <Link className="button ghost compact" to={`/servers/${item.server_id}/activity`}>Server activity <ArrowRight size={13} /></Link>}
            </article>
          ))}
        </section>
      ) : (
        <section className="panel"><EmptyState icon={Activity} title="No activity yet" description="Server actions, schedules, and important events will appear here." /></section>
      )}
    </>
  );
}

function PanelSettingsPage() {
  const session = useSession();
  const [showClientID, setShowClientID] = useState(false);
  const setup = useQuery({
    queryKey: ["setup-status", "panel-settings"],
    queryFn: () => api<SetupStatus>("/api/v1/setup/status"),
  });
  const installation = useQuery({
    queryKey: ["installation-settings"],
    queryFn: () => api<{
      mfa_policy: string;
      public_url: string;
      discord_client_id: string;
      discord_secret_configured: boolean;
    }>("/api/v1/installation/settings"),
    enabled: session.data?.user.panel_role === "owner",
  });
  if (session.isLoading) return <PageLoading title="Panel settings" />;
  if (session.data?.user.panel_role !== "owner") {
    return (
      <>
        <PageHeader eyebrow="INSTALLATION" title="Panel settings" description="Installation-wide security and access defaults." />
        <section className="panel"><EmptyState icon={ShieldCheck} title="Owner access required" description="Only the installation owner can change panel-wide settings." /></section>
      </>
    );
  }
  if (setup.isError || installation.isError) {
    return <ErrorPanel error={setup.error || installation.error} retry={() => { void setup.refetch(); void installation.refetch(); }} />;
  }
  const callback = setup.data ? `${setup.data.public_url}/api/v1/auth/discord/callback` : "Loading…";
  return (
    <>
      <PageHeader eyebrow="INSTALLATION" title="Panel settings" description="Review the fixed installation origin and configure Discord security policy." />
      {installation.data && <MFASettings policy={installation.data.mfa_policy} />}
      <PanelUpdateSettings />
      <section className="panel installation-details">
        <div className="panel-heading"><div><span className="eyebrow">AUTHENTICATION</span><h2>Discord OAuth2 application</h2></div><StatusBadge tone="success">identify only</StatusBadge></div>
        <dl className="host-details">
          <div><dt>Panel URL</dt><dd>{setup.data?.public_url ?? "Loading…"}</dd></div>
          <div>
            <dt>Discord client ID</dt>
            <dd className="concealed-value">
              <code>{setup.data
                ? showClientID
                  ? setup.data.discord_client_id
                  : "••••••••••••••••••"
                : "Loading…"}</code>
              {setup.data && (
                <button
                  type="button"
                  className="icon-button"
                  aria-label={showClientID ? "Hide Discord client ID" : "Show Discord client ID"}
                  title={showClientID ? "Hide Discord client ID" : "Show Discord client ID"}
                  aria-pressed={showClientID}
                  onClick={() => setShowClientID((current) => !current)}
                >
                  {showClientID ? <EyeOff size={16} /> : <Eye size={16} />}
                </button>
              )}
            </dd>
          </div>
          <div><dt>Exact callback URI</dt><dd><code>{callback}</code></dd></div>
          <div><dt>Invitation delivery</dt><dd>Expiring single-use links; no Discord bot or DMs</dd></div>
          <div><dt>Owner bootstrap</dt><dd>{setup.data?.bootstrap_enabled ? "Awaiting first claim" : "Consumed and disabled"}</dd></div>
        </dl>
      </section>
      {installation.data && <DiscordOAuthSettings settings={installation.data} />}
      <section className="notice info">
        The public URL is selected during installation because it defines trusted browser origins, cookie security, and reverse-proxy routing. Discord credentials can be rotated above without restarting the panel.
      </section>
    </>
  );
}

function PanelUpdateSettings() {
  const queryClient = useQueryClient();
  const [includePrereleases, setIncludePrereleases] = useState(true);
  const update = useQuery({
    queryKey: ["panel-update", includePrereleases],
    queryFn: () => api<PanelUpdate>(`/api/v1/installation/update?include_prereleases=${includePrereleases}`),
    refetchInterval: (query) => ["queued", "running"].includes(query.state.data?.status.state ?? "") ? 3_000 : 60_000,
    retry: (count, error) => count < 12 && error instanceof ApiError && error.status >= 500,
  });
  const check = useMutation({
    mutationFn: () => api<PanelUpdate>("/api/v1/installation/update/check", {
      method: "POST",
      body: JSON.stringify({ include_prereleases: includePrereleases }),
    }),
    onSuccess: (result) => queryClient.setQueryData(["panel-update", includePrereleases], result),
  });
  const apply = useMutation({
    mutationFn: (version: string) => api<PanelUpdate["status"]>("/api/v1/installation/update/apply", {
      method: "POST",
      body: JSON.stringify({ version, include_prereleases: includePrereleases }),
    }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["panel-update"] });
    },
  });
  const data = update.data;
  const active = ["queued", "running"].includes(data?.status.state ?? "");
  const latest = data?.check.latest;
  const canApply = Boolean(data?.check.updates_supported && data.check.update_available && latest && !active && !apply.isPending);
  const requestUpdate = () => {
    if (!latest) return;
    const warning = [
      `Update Dockside from ${data?.build.version ?? "the current version"} to ${latest.version}?`,
      "",
      "The panel, worker, and running game servers will be stopped temporarily.",
      "Dockside will first create one local pre-update recovery snapshot containing panel configuration and secrets, PostgreSQL, managed container images/configuration, system volumes, and all managed game-server volumes.",
      "",
      "Do not power off the host during this operation.",
    ].join("\n");
    if (window.confirm(warning)) apply.mutate(latest.version);
  };
  const statusTone: "danger" | "success" | "warning" | "info" = data?.status.state === "failed"
    ? "danger"
    : data?.status.state === "succeeded"
      ? "success"
      : active
        ? "warning"
        : "info";
  return (
    <section className="panel panel-update-settings">
      <div className="panel-heading">
        <div><span className="eyebrow">SOFTWARE</span><h2>Panel version & updates</h2></div>
        <StatusBadge tone={statusTone}>{active ? data?.status.phase ?? "updating" : data?.status.state ?? "checking"}</StatusBadge>
      </div>
      {update.isLoading ? <TableLoading /> : update.isError ? (
        <div className="panel-update-body"><ErrorPanel error={update.error} retry={() => void update.refetch()} /></div>
      ) : data ? (
        <div className="panel-update-body">
          <div className="panel-update-version">
            <span className="panel-update-icon"><PackageCheck size={24} /></span>
            <div>
              <span>Running version</span>
              <strong>{data.build.version}</strong>
              <small>Revision {data.build.revision === "unknown" ? "not embedded" : data.build.revision.slice(0, 12)} · Built {data.build.built_at === "unknown" ? "from source" : formatDate(data.build.built_at)}</small>
            </div>
          </div>
          <div className="panel-update-controls">
            <label className="checkbox-row">
              <input type="checkbox" checked={includePrereleases} disabled={active} onChange={(event) => setIncludePrereleases(event.target.checked)} />
              <span>Include alpha, beta, and release-candidate updates</span>
            </label>
            <button type="button" className="button secondary" disabled={check.isPending || active} onClick={() => check.mutate()}>
              <RotateCw size={16} className={check.isPending ? "spin" : undefined} /> {check.isPending ? "Checking…" : "Check for updates"}
            </button>
          </div>
          {!data.check.updates_supported ? (
            <div className="notice warning">{data.check.reason}</div>
          ) : latest ? (
            <div className={`panel-release-card ${data.check.update_available ? "available" : ""}`}>
              <div>
                <span>{data.check.update_available ? "Update available" : "Latest published release"}</span>
                <strong>{latest.name}</strong>
                <small>{latest.prerelease ? "Pre-release" : "Stable"} · Published {formatDate(latest.published_at)}</small>
              </div>
              <div className="panel-release-actions">
                <a className="button ghost" href={latest.url} target="_blank" rel="noreferrer">Release notes <ExternalLink size={15} /></a>
                {data.check.update_available && (
                  <button type="button" className="button primary" disabled={!canApply} onClick={requestUpdate}>
                    <Download size={16} /> {apply.isPending ? "Starting…" : `Update to ${latest.version}`}
                  </button>
                )}
              </div>
            </div>
          ) : (
            <div className="notice info">No complete Dockside release with a signed archive and checksums is published for this channel.</div>
          )}
          {(active || data.status.state === "failed" || data.status.state === "succeeded") && (
            <div className={`panel-update-progress ${data.status.state}`}>
              <div><strong>{data.status.message || "Update status"}</strong><StatusBadge tone={statusTone}>{data.status.phase || data.status.state}</StatusBadge></div>
              {active && <span className="indeterminate-progress"><i /></span>}
              <small>Target {data.status.target_version || "—"} · Last update {formatDate(data.status.updated_at)}</small>
              {data.status.snapshot_path && <small>Recovery snapshot: <code>{data.status.snapshot_path}</code></small>}
              {data.status.error && <div className="form-error">{data.status.error}</div>}
              {data.status.failure_recovery && <div className="notice warning">{data.status.failure_recovery}</div>}
            </div>
          )}
          {apply.isError && <div className="form-error">{apply.error.message}</div>}
          <p className="panel-update-footnote">
            The updater accepts only published Dockside.GG GitHub releases, validates the versioned ZIP against <code>SHA256SUMS</code>, and retains exactly one completed pre-update snapshot. Existing game backups are left in place.
          </p>
        </div>
      ) : null}
    </section>
  );
}

function DiscordOAuthSettings({ settings }: {
  settings: {
    public_url: string;
    discord_client_id: string;
    discord_secret_configured: boolean;
  };
}) {
  const queryClient = useQueryClient();
  const [clientID, setClientID] = useState(settings.discord_client_id);
  const [clientSecret, setClientSecret] = useState("");
  const [showClientID, setShowClientID] = useState(false);
  const [showSecret, setShowSecret] = useState(false);
  const [copied, setCopied] = useState(false);
  const callback = `${settings.public_url}/api/v1/auth/discord/callback`;
  const save = useMutation({
    mutationFn: () => api<void>("/api/v1/installation/settings", {
      method: "PUT",
      body: JSON.stringify({
        discord_client_id: clientID.trim(),
        discord_client_secret: clientSecret.trim() || undefined,
      }),
    }),
    onSuccess: () => {
      setClientSecret("");
      void queryClient.invalidateQueries({ queryKey: ["installation-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["setup-status"] });
    },
  });
  return (
    <section className="panel discord-settings">
      <div className="panel-heading"><div><span className="eyebrow">EDIT AUTHENTICATION</span><h2>Discord credentials</h2></div><StatusBadge tone="success">Encrypted at rest</StatusBadge></div>
      <div className="configuration-form">
        <label>
          Discord client ID
          <span className="secret-input">
            <input type={showClientID ? "text" : "password"} inputMode="numeric" value={clientID} onChange={(event) => setClientID(event.target.value.replace(/\D/g, ""))} />
            <button type="button" className="icon-button" aria-label={showClientID ? "Hide Discord client ID" : "Show Discord client ID"} onClick={() => setShowClientID((value) => !value)}>{showClientID ? <EyeOff size={17} /> : <Eye size={17} />}</button>
          </span>
        </label>
        <label>
          Replace Discord client secret
          <span className="label-hint">Leave blank to keep the existing encrypted value. The saved secret is never returned to the browser.</span>
          <span className="secret-input">
            <input type={showSecret ? "text" : "password"} autoComplete="new-password" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} placeholder={settings.discord_secret_configured ? "Secret configured" : "Enter client secret"} />
            <button type="button" className="icon-button" aria-label={showSecret ? "Hide new Discord client secret" : "Show new Discord client secret"} onClick={() => setShowSecret((value) => !value)}>{showSecret ? <EyeOff size={17} /> : <Eye size={17} />}</button>
          </span>
        </label>
        <label>
          Exact callback URI
          <span className="copy-value"><code>{callback}</code><button type="button" className="button ghost compact" onClick={() => {
            void navigator.clipboard.writeText(callback);
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1800);
          }}>{copied ? <Check size={15} /> : <Copy size={15} />} {copied ? "Copied" : "Copy"}</button></span>
        </label>
        <div className="notice info">Add this exact callback URI to the OAuth2 redirect list in the Discord developer portal. Changes take effect on the next sign-in attempt.</div>
        {save.isError && <div className="form-error">{save.error.message}</div>}
        {save.isSuccess && <div className="form-success">Discord authentication settings saved.</div>}
        <div className="configuration-actions">
          <button className="button primary" disabled={save.isPending || clientID.length < 17} onClick={() => save.mutate()}><ShieldCheck size={16} /> {save.isPending ? "Saving…" : "Save Discord settings"}</button>
        </div>
      </div>
    </section>
  );
}

function DiagnosticsPage() {
  const session = useSession();
  const [component, setComponent] = useState("app");
  const diagnostics = useQuery({
    queryKey: ["diagnostics"],
    queryFn: () => api<{ build: BuildInfo; entries: DiagnosticEntry[] }>("/api/v1/diagnostics?limit=250"),
    enabled: ["owner", "administrator"].includes(session.data?.user.panel_role ?? ""),
    refetchInterval: 15_000,
  });
  const containers = useQuery({
    queryKey: ["system-containers"],
    queryFn: () => api<{ containers: SystemContainer[] }>("/api/v1/system/containers"),
    enabled: ["owner", "administrator"].includes(session.data?.user.panel_role ?? ""),
    refetchInterval: 15_000,
  });
  const logs = useQuery({
    queryKey: ["system-container-logs", component],
    queryFn: () => api<SystemContainerLogs>(`/api/v1/system/containers/${component}/logs?tail=500`),
    enabled: ["owner", "administrator"].includes(session.data?.user.panel_role ?? "") && Boolean(component),
  });
  if (session.isLoading) return <PageLoading title="Diagnostics" />;
  if (!["owner", "administrator"].includes(session.data?.user.panel_role ?? "")) {
    return (
      <>
        <PageHeader eyebrow="OPERATIONS" title="Diagnostics" description="Panel, engine, worker, and runtime failures." />
        <section className="panel"><EmptyState icon={ShieldCheck} title="Administrator access required" description="The owner controls access by assigning the administrator panel role." /></section>
      </>
    );
  }
  const availableComponents = containers.data?.containers.map((item) => item.component) ?? [
    "app", "worker", "engine", "gateway", "postgres",
  ];
  return (
    <>
      <PageHeader
        eyebrow="OPERATIONS"
        title="Diagnostics"
        description="Sanitized panel, worker, engine, and Docker control-plane failures. Game process output remains in each server console and is never reclassified by the panel."
        actions={<button className="button secondary" onClick={() => {
          void diagnostics.refetch();
          void containers.refetch();
          void logs.refetch();
        }}><RotateCw size={15} /> Refresh</button>}
      />
      {diagnostics.data?.build && (
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">BUILD</span><h2>Dockside release</h2></div><StatusBadge tone={diagnostics.data.build.version === "dev" ? "warning" : "success"}>{diagnostics.data.build.version}</StatusBadge></div>
          <dl className="detail-list">
            <div><dt>Version</dt><dd>{diagnostics.data.build.version}</dd></div>
            <div><dt>Revision</dt><dd className="mono">{diagnostics.data.build.revision}</dd></div>
            <div><dt>Built</dt><dd>{diagnostics.data.build.built_at === "unknown" ? "Development build" : diagnostics.data.build.built_at}</dd></div>
          </dl>
        </section>
      )}
      <div className="diagnostics-layout">
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">CONTROL PLANE</span><h2>Operational failures</h2></div><StatusBadge tone={diagnostics.data?.entries.length ? "warning" : "success"}>{diagnostics.data?.entries.length ?? 0} recent</StatusBadge></div>
          {diagnostics.isLoading && <TableLoading />}
          {diagnostics.isError && <ErrorPanel error={diagnostics.error} retry={() => void diagnostics.refetch()} />}
          {diagnostics.data?.entries.length === 0 && <EmptyState icon={ShieldCheck} title="No operational failures" description="No failed operations, runtime infrastructure errors, or blocked background jobs were found." />}
          <div className="diagnostic-list">
            {diagnostics.data?.entries.map((item, index) => (
              <article key={`${item.source}-${item.created_at}-${index}`}>
                <FileWarning size={17} />
                <div>
                  <div><strong>{item.summary}</strong><StatusBadge tone={item.severity === "error" ? "danger" : "warning"}>{item.source}</StatusBadge></div>
                  <code>{item.code}</code>
                  {item.detail && <pre>{item.detail}</pre>}
                  <span>{formatDate(item.created_at)}{item.server_id ? <> · <Link to={`/servers/${item.server_id}/activity`}>Open server</Link></> : null}</span>
                </div>
              </article>
            ))}
          </div>
        </section>
        <section className="panel system-log-panel">
          <div className="panel-heading">
            <div><span className="eyebrow">CONTAINER LOGS</span><h2>Dockside services</h2></div>
            <select aria-label="System container" value={component} onChange={(event) => setComponent(event.target.value)}>
              {availableComponents.map((item) => <option value={item} key={item}>{item}</option>)}
            </select>
          </div>
          <p className="section-description">Read-only tail from the selected system container. The web UI cannot stop or restart the app, engine, gateway, or database.</p>
          {logs.isLoading && <TableLoading />}
          {logs.isError && <ErrorPanel error={logs.error} retry={() => void logs.refetch()} />}
          {logs.data && (
            <div className="system-log-streams">
              <details open><summary>Standard output</summary><pre>{logs.data.stdout || "No stdout in this tail."}</pre></details>
              <details open={Boolean(logs.data.stderr)}><summary>Standard error</summary><pre>{logs.data.stderr || "No stderr in this tail."}</pre></details>
            </div>
          )}
        </section>
      </div>
    </>
  );
}

function PageLoading({ title }: { title: string }) {
  return (
    <>
      <PageHeader title={title} />
      <div className="skeleton-grid"><span /><span /><span /><span /></div>
    </>
  );
}

function UsageMeter({ label, value, detail, available = true }: {
  label: string;
  value: number;
  detail: string;
  available?: boolean;
}) {
  const normalized = available ? Math.max(0, Math.min(value, 100)) : 0;
  const tone = normalized >= 90 ? "danger" : normalized >= 75 ? "warning" : "normal";
  return (
    <div className={`usage-meter ${tone} ${available ? "" : "unavailable"}`}>
      <div><span>{label}</span><strong>{available ? formatPercent(normalized) : "—"}</strong></div>
      <span className="usage-meter-track" role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={available ? normalized : undefined}>
        <span style={{ width: `${normalized}%` }} />
      </span>
      <small>{detail}</small>
    </div>
  );
}

const systemContainerHelp: Record<SystemContainer["component"], string> = {
  gateway: "Receives HTTP and HTTPS traffic and routes requests to the Dockside application.",
  app: "Serves the panel interface and API, including Discord sign-in and permission checks. It does not access Docker directly.",
  worker: "Runs provisioning, schedules, backups, webhooks, telemetry synchronization, and automatic server recovery.",
  engine: "The isolated Docker control service used for container, console, file, backup, and network operations.",
  postgres: "Stores panel users, configuration, templates, servers, jobs, and audit history. Game databases are separate.",
};

function InfoTooltip({ label, description }: { label: string; description: string }) {
  const [open, setOpen] = useState(false);
  return (
    <span className={`info-tooltip ${open ? "open" : ""}`}>
      <button
        type="button"
        aria-label={`About the ${label} container`}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
        }}
      >
        <Info size={13} />
      </button>
      <span role="tooltip">{description}</span>
    </span>
  );
}

function SystemContainersPanel({
  containers,
  error,
  canRestartWorker,
  restarting,
  restartError,
  onRestartWorker,
}: {
  containers: SystemContainer[];
  error?: string;
  canRestartWorker: boolean;
  restarting: boolean;
  restartError?: string;
  onRestartWorker: () => void;
}) {
  return (
    <section className="panel system-containers-panel">
      <div className="panel-heading">
        <div>
          <span className="eyebrow">PRIVILEGED HEALTH</span>
          <h2>Dockside system containers</h2>
        </div>
        <StatusBadge tone={error || containers.some((item) => item.state !== "running") ? "warning" : "success"}>
          {error ? "Unavailable" : `${containers.length} components`}
        </StatusBadge>
      </div>
      <p className="system-container-boundary">
        Control-plane services are health-only. The panel cannot stop, kill, or restart the app, gateway, engine, or database.
      </p>
      {error ? (
        <div className="notice warning">{error}</div>
      ) : (
        <div className="system-container-grid">
          {containers.map((item) => {
            const healthy = item.state === "running" && (item.health === "healthy" || item.health === "unknown");
            return (
              <article key={item.component}>
                <div className="system-container-heading">
                  <div>
                    <Boxes size={17} />
                    <strong>{item.component}</strong>
                    <InfoTooltip
                      label={item.component}
                      description={systemContainerHelp[item.component]}
                    />
                  </div>
                  <StatusBadge tone={healthy ? "success" : item.state === "running" ? "warning" : "danger"}>
                    {item.health !== "unknown" ? item.health : item.state}
                  </StatusBadge>
                </div>
                <div className="system-container-metrics">
                  <span><small>CPU</small><strong>{formatPercent(item.cpu_percent)}</strong></span>
                  <span><small>Memory</small><strong>{formatBytes(item.memory_bytes)}</strong></span>
                  <span><small>Network</small><strong>{formatBytes(item.network_rx_bytes)} ↓ / {formatBytes(item.network_tx_bytes)} ↑</strong></span>
                  <span><small>Disk I/O</small><strong>{formatBytes(item.block_read_bytes)} ↓ / {formatBytes(item.block_write_bytes)} ↑</strong></span>
                </div>
                <div className="system-container-footer">
                  <span title={item.image}>{item.image}</span>
                  {item.component === "worker" && canRestartWorker && (
                    <button className="button small secondary" disabled={restarting} onClick={onRestartWorker}>
                      <RotateCw size={13} className={restarting ? "spin-icon" : undefined} />
                      {restarting ? "Restarting…" : "Restart worker"}
                    </button>
                  )}
                </div>
                {item.error && <small className="container-error">{item.error}</small>}
              </article>
            );
          })}
        </div>
      )}
      {restartError && <div className="form-error">{restartError}</div>}
    </section>
  );
}

function ServerStatus({ server }: { server: ServerSummary }) {
  const presentation = serverPresentation(server);
  return <StatusBadge tone={presentation.tone}>{presentation.label}</StatusBadge>;
}

function MetricCard({ label, value, icon: Icon, accent }: { label: string; value: number; icon: typeof Server; accent: string }) {
  return <article className={`metric-card ${accent}`}><span className="metric-icon"><Icon size={21} /></span><div><strong>{value}</strong><span>{label}</span></div></article>;
}

function HostItem({ icon: Icon, label, value }: { icon: typeof Cpu; label: string; value: string | number }) {
  return <div className="host-item"><Icon size={19} /><div><span>{label}</span><strong>{value}</strong></div></div>;
}

function TableLoading() {
  return <div className="table-loading"><span /><span /><span /></div>;
}

function formatBytes(value?: number | null) {
  if (value == null) return "—";
  if (value === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatPercent(value?: number | null) {
  return value == null ? "—" : `${value.toFixed(1)}%`;
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

export function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route path="/invite/:token" element={<InvitePage />} />
      <Route path="/pending" element={<PendingPage />} />
      <Route element={<ProtectedLayout />}>
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/servers" element={<ServersPage />} />
        <Route path="/servers/new" element={<NewServerPage />} />
        <Route path="/servers/:serverID/*" element={<ServerDetailPage />} />
        <Route path="/templates" element={<TemplateLibraryPage />} />
        <Route path="/templates/new" element={<TemplateEditorPage create />} />
        <Route path="/templates/:versionID" element={<TemplateDetailPage />} />
        <Route path="/templates/:versionID/edit" element={<TemplateEditorPage />} />
        <Route path="/users" element={<UsersPage />} />
        <Route path="/activity" element={<ActivityPage />} />
        <Route path="/diagnostics" element={<DiagnosticsPage />} />
        <Route path="/settings" element={<PanelSettingsPage />} />
      </Route>
      <Route path="*" element={<Navigate to="/dashboard" replace />} />
    </Routes>
  );
}
