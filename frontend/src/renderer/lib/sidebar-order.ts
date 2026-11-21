import {
	attentionZone,
	sessionNeedsAttention,
	sortedWorkerSessions,
	workerSessions,
	ownScratchpadWorkspaces,
	STANDALONE_PROJECT_KIND,
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { sessionUiKey } from "./hosts";

/**
 * Items listed in `order` keep that order; items it has never seen are placed
 * before (`"start"`) or after (`"end"`) the ones it has. The sidebar uses this
 * for drag-reordered projects and sessions.
 */
export function applyOrder<T>(
	items: readonly T[],
	idOf: (item: T) => string,
	order: readonly string[],
	unplaced: "start" | "end",
): T[] {
	if (order.length === 0) return [...items];
	const byId = new Map(items.map((item) => [idOf(item), item]));
	const placed = order.flatMap((id) => {
		const item = byId.get(id);
		return item ? [item] : [];
	});
	const placedIds = new Set(order);
	const rest = items.filter((item) => !placedIds.has(idOf(item)));
	return unplaced === "start" ? [...rest, ...placed] : [...placed, ...rest];
}

/** Stable partition: pinned ids first, each group keeping its incoming order. */
export function pinnedFirst<T>(items: readonly T[], idOf: (item: T) => string, pinnedIds: readonly string[]): T[] {
	if (pinnedIds.length === 0) return [...items];
	const pinned = new Set(pinnedIds);
	return [...items.filter((item) => pinned.has(idOf(item))), ...items.filter((item) => !pinned.has(idOf(item)))];
}

export type SidebarNavSession = {
	sessionId: string;
	projectId: string;
	hostId?: string;
	session: WorkspaceSession;
};

export type SidebarOrderInput = {
	workspaces: readonly WorkspaceSummary[];
	remoteWorkspaces?: readonly WorkspaceSummary[];
	/** Saved drag order of the project list. */
	projectOrder?: readonly string[];
	/** Projects pinned to the top of the list. */
	pinnedProjectIds?: readonly string[];
	/** Drag order of each project's sessions, keyed by `sessionUiKey(projectId, hostId)`. */
	sessionOrders?: Readonly<Record<string, readonly string[]>>;
};

/** Sessions the sidebar lists for one project: workers only, terminated ones dropped. */
function listedSessions(
	workspace: WorkspaceSummary,
	sessionOrders: SidebarOrderInput["sessionOrders"],
): WorkspaceSession[] {
	const visible = sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true);
	const stored = sessionOrders?.[sessionUiKey(workspace.id, workspace.hostId)] ?? [];
	if (stored.length > 0) {
		// The sidebar publishes the exact rows it shows, including rows the
		// worker-only default omits (a team project's orchestrator). Keyboard
		// navigation must walk the same set, so admit stored rows back here.
		// Projects without stored orders keep the worker-only default.
		const known = new Set(visible.map((session) => session.id));
		const byId = new Map(workspace.sessions.map((session) => [session.id, session]));
		for (const id of stored) {
			const extra = byId.get(id);
			if (extra && extra.isTerminated !== true && !known.has(id)) {
				known.add(id);
				visible.push(extra);
			}
		}
	}
	return applyOrder(visible, (session) => session.id, stored, "start");
}

/** Minimal team agent shape for sidebar hierarchy (satisfied by TeamView Agent). */
export type TeamHierarchyAgent = {
	id: string;
	tier: string;
	parent: string | null;
	session_id: string | null;
};

export type TeamSessionMeta = {
	/** Team role tier of the agent attached to this session. */
	tier: string;
	/** Attached session of the supervisor agent, when the supervisor has one. */
	supervisorSessionId: string | null;
};

/** Index attached team agents by session id. Detached agents never appear. */
export function buildTeamSessionMeta(agents: readonly TeamHierarchyAgent[]): Map<string, TeamSessionMeta> {
	const byId = new Map(agents.map((agent) => [agent.id, agent]));
	const meta = new Map<string, TeamSessionMeta>();
	for (const agent of agents) {
		if (!agent.session_id) continue;
		const supervisor = agent.parent ? byId.get(agent.parent) : undefined;
		meta.set(agent.session_id, { tier: agent.tier, supervisorSessionId: supervisor?.session_id ?? null });
	}
	return meta;
}

/** Attached orchestrator session named by the team metadata, if any. */
export function teamOrchestratorSessionId(meta: ReadonlyMap<string, TeamSessionMeta>): string | null {
	for (const [sessionId, info] of meta) {
		if (info.tier === "orchestrator") return sessionId;
	}
	return null;
}

export type TeamRowDensity = "standard" | "chief" | "worker";

/** Row density for a team role tier. Unknown tiers keep the standard height. */
export function densityForTier(tier: string | undefined): TeamRowDensity {
	return tier === "chief" ? "chief" : tier === "worker" ? "worker" : "standard";
}

export type TeamRowMeta = {
	density: TeamRowDensity;
	/** Reporting-line depth: crews under the orchestrator sit at 1, chief crews at 2. */
	depth: 0 | 1 | 2;
};

/**
 * Group sidebar sessions by team reporting line: the orchestrator first, the
 * planner as its top-level peer, the orchestrator's direct crew, then each
 * chief with its crew, then workers whose supervisor has no visible session
 * (reachable, never misattributed to another crew), then ordinary sessions.
 * Every group keeps the incoming relative order, so manual sorts and attention
 * floats move whole crews without ever splitting one from its chief.
 */
export function applyTeamHierarchy<T extends { id: string }>(
	sessions: readonly T[],
	meta: ReadonlyMap<string, TeamSessionMeta>,
): { sessions: T[]; rowMeta: Map<string, TeamRowMeta> } {
	const byId = new Map(sessions.map((session) => [session.id, session]));
	const rows: Array<{ session: T; meta: TeamRowMeta }> = [];
	const placed = new Set<string>();
	const place = (session: T, density: TeamRowDensity, depth: 0 | 1 | 2) => {
		if (placed.has(session.id)) return;
		placed.add(session.id);
		rows.push({ session, meta: { density, depth } });
	};
	const crewOf = (supervisorSessionId: string): T[] =>
		sessions.filter(
			(session) => meta.get(session.id)?.tier === "worker" && meta.get(session.id)?.supervisorSessionId === supervisorSessionId,
		);
	const orchId = teamOrchestratorSessionId(meta);
	if (orchId !== null && byId.has(orchId)) {
		place(byId.get(orchId)!, "standard", 0);
	}
	for (const session of sessions) {
		if (meta.get(session.id)?.tier === "planner") place(session, "standard", 0);
	}
	// The orchestrator's crew reads with its supervisor: it follows the planner
	// peer so the planner stays immediately below the orchestrator.
	if (orchId !== null && byId.has(orchId)) {
		for (const member of crewOf(orchId)) place(member, "worker", 1);
	}
	for (const session of sessions) {
		if (meta.get(session.id)?.tier !== "chief") continue;
		place(session, "chief", 0);
		for (const member of crewOf(session.id)) place(member, "worker", 2);
	}
	for (const session of sessions) {
		if (placed.has(session.id) || meta.get(session.id)?.tier !== "worker") continue;
		place(session, "worker", 0);
	}
	for (const session of sessions) {
		if (!placed.has(session.id)) place(session, "standard", 0);
	}
	return {
		sessions: rows.map((row) => row.session),
		rowMeta: new Map(rows.map((row) => [row.session.id, row.meta])),
	};
}

/**
 * Every session reachable from the sidebar, in the order the sidebar shows
 * them: pinned sessions, then Sessions (the projectless agents), then projects
 * (pinned first) in the user's order, then remote hosts. A session that appears
 * twice (pinned and under its project) counts once, at its first position. Keyboard navigation and the
 * ⌘1–9 jump both walk this list so they always agree with what is on screen.
 */
export function buildSidebarSessionOrder({
	workspaces,
	remoteWorkspaces = [],
	projectOrder = [],
	pinnedProjectIds = [],
	sessionOrders,
}: SidebarOrderInput): SidebarNavSession[] {
	const entries: SidebarNavSession[] = [];
	const seen = new Set<string>();
	const add = (session: WorkspaceSession, workspace: WorkspaceSummary) => {
		const key = sessionUiKey(session.id, session.hostId ?? workspace.hostId);
		if (seen.has(key)) return;
		seen.add(key);
		entries.push({
			sessionId: session.id,
			projectId: workspace.id,
			hostId: session.hostId ?? workspace.hostId,
			session,
		});
	};

	const pinned = [...workspaces, ...remoteWorkspaces]
		.flatMap((workspace) => workerSessions(workspace.sessions).map((session) => ({ workspace, session })))
		.filter(({ session }) => session.isPinned && session.isTerminated !== true)
		.sort((a, b) => pinnedTime(b.session) - pinnedTime(a.session));
	for (const { workspace, session } of pinned) add(session, workspace);

	const scratchpads = ownScratchpadWorkspaces(workspaces);
	// The Sessions section, above Projects, lists its local sessions and the
	// person's Cloud standalone agents as one list, in its own order; each agent
	// still routes by its project.
	const scratchpadOf = new Map(scratchpads.flatMap((workspace) => workspace.sessions.map((session) => [session.id, workspace] as const)));
	const scratchpad: WorkspaceSummary = {
		...(scratchpads.find((workspace) => workspace.kind === STANDALONE_PROJECT_KIND) ??
			{ id: STANDALONE_WORKSPACE_ID, name: "", kind: STANDALONE_PROJECT_KIND, path: "" }),
		sessions: scratchpads.flatMap((workspace) => workspace.sessions),
	};
	for (const session of listedSessions(scratchpad, sessionOrders)) add(session, scratchpadOf.get(session.id) ?? scratchpad);
	const projects = workspaces.filter((workspace) => !scratchpads.includes(workspace));
	const orderedProjects = pinnedFirst(applyOrder(projects, (item) => item.id, projectOrder, "start"), (item) => item.id, pinnedProjectIds);
	for (const workspace of orderedProjects) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	for (const workspace of remoteWorkspaces) {
		for (const session of listedSessions(workspace, sessionOrders)) add(session, workspace);
	}
	return entries;
}

function pinnedTime(session: WorkspaceSession): number {
	return session.pinnedAt ? new Date(session.pinnedAt).getTime() || 0 : 0;
}

export function sessionWantsAttention(session: WorkspaceSession): boolean {
	return attentionZone(session) === "merge" || sessionNeedsAttention(session);
}

type NavCursor = { sessionId?: string; hostId?: string };

function indexOfCurrent<T extends { sessionId: string; hostId?: string }>(list: readonly T[], current: NavCursor): number {
	if (!current.sessionId) return -1;
	return list.findIndex((entry) => entry.sessionId === current.sessionId && (entry.hostId ?? "") === (current.hostId ?? ""));
}

/**
 * The entry `direction` steps from the current session, wrapping at the ends,
 * optionally skipping entries that fail `accept`. With no current session
 * (home, a project board) forward starts at the first entry and backward at
 * the last. The current session itself is never returned.
 */
export function stepSessionNav<T extends { sessionId: string; hostId?: string }>(
	list: readonly T[],
	current: NavCursor,
	direction: -1 | 1,
	accept: (entry: T) => boolean = () => true,
): T | undefined {
	const currentIndex = indexOfCurrent(list, current);
	const start = currentIndex === -1 ? (direction === 1 ? -1 : list.length) : currentIndex;
	for (let step = 1; step <= list.length; step += 1) {
		const index = (((start + direction * step) % list.length) + list.length) % list.length;
		if (index === currentIndex) return undefined;
		if (accept(list[index])) return list[index];
	}
	return undefined;
}
