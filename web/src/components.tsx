import type { ReactNode } from "react";
import {
  Activity,
  Boxes,
  ChevronRight,
  LayoutDashboard,
  Library,
  LogOut,
  Menu,
  Server,
  Settings,
  ShieldCheck,
  Users,
  X,
} from "lucide-react";
import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import type { Session } from "./types";

export function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <div className="brand" aria-label="Dockside Game Panel">
      <span className="brand-mark">
        <span />
        <span />
        <span />
      </span>
      {!compact && (
        <span className="brand-name">
          Dockside<span>.GG</span>
          <small>GAME PANEL</small>
        </span>
      )}
    </div>
  );
}

const navigation = [
  { to: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { to: "/servers", label: "Servers", icon: Server },
  { to: "/templates", label: "Templates", icon: Library },
  { to: "/users", label: "Users & access", icon: Users, ownerOnly: true },
  { to: "/activity", label: "Activity", icon: Activity },
  { to: "/settings", label: "Panel settings", icon: Settings, ownerOnly: true },
];

export function AppShell({ session }: { session: Session }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const logout = useMutation({
    mutationFn: () => api<void>("/api/v1/auth/logout", { method: "POST" }),
    onSuccess: async () => {
      queryClient.clear();
      await navigate("/login", { replace: true });
    },
  });
  const displayName = session.user.global_name || session.user.username;

  return (
    <div className="app-shell">
      <button
        className="mobile-menu"
        type="button"
        aria-label="Open navigation"
        onClick={() => setMobileOpen(true)}
      >
        <Menu size={21} />
      </button>
      {mobileOpen && (
        <button
          className="mobile-backdrop"
          type="button"
          aria-label="Close navigation"
          onClick={() => setMobileOpen(false)}
        />
      )}
      <aside className={`sidebar ${mobileOpen ? "is-open" : ""}`}>
        <div className="sidebar-header">
          <Brand />
          <button
            className="icon-button sidebar-close"
            type="button"
            aria-label="Close navigation"
            onClick={() => setMobileOpen(false)}
          >
            <X size={19} />
          </button>
        </div>
        <div className="instance-chip">
          <span className="pulse-dot" />
          Local engine connected
        </div>
        <nav aria-label="Main navigation">
          {navigation
            .filter((item) => !item.ownerOnly || session.user.panel_role === "owner")
            .map(({ to, label, icon: Icon }) => (
            <NavLink
              key={to}
              to={to}
              onClick={() => setMobileOpen(false)}
              className={({ isActive }) => (isActive ? "active" : undefined)}
            >
              <Icon size={19} strokeWidth={1.8} />
              <span>{label}</span>
              <ChevronRight className="nav-chevron" size={16} />
            </NavLink>
            ))}
        </nav>
        <div className="sidebar-footer">
          <div className="user-card">
            <UserAvatar user={session.user} />
            <div>
              <strong>{displayName}</strong>
              <span>{session.user.panel_role}</span>
            </div>
          </div>
          <button
            className="icon-button"
            type="button"
            aria-label="Sign out"
            title="Sign out"
            onClick={() => logout.mutate()}
            disabled={logout.isPending}
          >
            <LogOut size={18} />
          </button>
        </div>
      </aside>
      <main className="main-content">
        <Outlet />
      </main>
    </div>
  );
}

export function UserAvatar({
  user,
  size = 38,
}: {
  user: Session["user"];
  size?: number;
}) {
  const source =
    user.avatar_hash &&
    `https://cdn.discordapp.com/avatars/${user.discord_id}/${user.avatar_hash}.png?size=80`;
  const name = user.global_name || user.username;
  return source ? (
    <img
      className="avatar"
      src={source}
      alt=""
      width={size}
      height={size}
      referrerPolicy="no-referrer"
    />
  ) : (
    <span className="avatar avatar-fallback" style={{ width: size, height: size }}>
      {name.slice(0, 2).toUpperCase()}
    </span>
  );
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        {eyebrow && <span className="eyebrow">{eyebrow}</span>}
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}

export function EmptyState({
  icon: Icon = Boxes,
  title,
  description,
  action,
}: {
  icon?: typeof Boxes;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <span className="empty-icon">
        <Icon size={26} />
      </span>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  );
}

export function StatusBadge({
  children,
  tone = "neutral",
}: {
  children: ReactNode;
  tone?: "neutral" | "success" | "warning" | "danger" | "info";
}) {
  return <span className={`badge ${tone}`}>{children}</span>;
}

export function LoadingScreen() {
  return (
    <div className="center-screen">
      <Brand />
      <span className="loader" aria-label="Loading" />
    </div>
  );
}

export function ErrorPanel({
  title = "We couldn't load this section",
  error,
  retry,
}: {
  title?: string;
  error: unknown;
  retry?: () => void;
}) {
  return (
    <div className="error-panel" role="alert">
      <ShieldCheck size={22} />
      <div>
        <strong>{title}</strong>
        <p>{error instanceof Error ? error.message : "An unexpected error occurred."}</p>
      </div>
      {retry && (
        <button className="button ghost small" type="button" onClick={retry}>
          Try again
        </button>
      )}
    </div>
  );
}
