import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { TeamPlanning } from "./TeamPlanning";
import { TeamOrgChart } from "./TeamOrgChart";
import { aoBridge } from "../lib/bridge";
import { MissionStaffingFields } from "./MissionStaffingFields";
import { MissionSubmitForm, ReviewForm } from "./TeamReview";
import { NameField, TeamDialog } from "./TeamDialog";
import { ChevronDown } from "@/components/app-icons";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "./ui/sheet";
import { uniqueId } from "../lib/team-ids";

type Role = { id: string; tier: string; instructions: string };
export type Agent = {
	id: string;
	tier: string;
	role: string;
	parent: string | null;
	provider: string;
	enabled: number;
	session_id: string | null;
	launch_error: string | null;
	revision: number;
};
export type Mission = {
	workers?: string[];
	id: string;
	chief: string;
	supervisor: string;
	orchestrator: string;
	objective: string;
	criteria: string;
	status: string;
	revision: number;
	result: string | null;
	evidence: string | null;
	review: string | null;
	plan?: string;
};
type Task = {
	id: string;
	mission: string;
	worker: string;
	objective: string;
	status: string;
	revision: number;
	result: string | null;
	evidence: string | null;
	review: string | null;
};
type Message = {
	id: string;
	sender: string;
	recipient: string;
	subject: string;
	body: string;
	created_at: string;
	delivered_at: string | null;
	acknowledged_at: string | null;
};
export type TeamState = {
	enabled: boolean;
	roles: Role[];
	agents: Agent[];
	missions: Mission[];
	drafts: MissionDraft[];
	tasks: Task[];
	messages: Message[];
	pending: Record<string, number>;
	principal?: { id: string; name: string };
};
export type MissionDraft = {
	workers?: string[];
	id: string;
	planner: string;
	chief: string | null;
	supervisor: string | null;
	objective: string;
	criteria: string;
	plan: string;
	status: string;
	revision: number;
	mission: string | null;
};
export type TeamAction = (operation: string, arguments_: Record<string, unknown>, sessionId?: string) => Promise<boolean>;
type Editor = "agent-add" | "role-add" | "mission-create" | "task-assign" | "send" | "enable";
type Option = string | { value: string; label: string };
type Field = {
	name: string;
	label: string;
	options?: Option[];
	multiline?: boolean;
	optional?: boolean;
	value?: string;
};

const optionValue = (option: Option) => (typeof option === "string" ? option : option.value);
const optionLabel = (option: Option) => (typeof option === "string" ? option : option.label);

export const providerLabel = (provider: string) =>
	provider === "muse" ? "Muse" : provider === "claude-code" ? "Claude Code" : "Codex";
const tierLabel = (tier: string) => tier.charAt(0).toUpperCase() + tier.slice(1);
const modeOption = (mode: string) => ({ value: mode, label: mode === "tui" ? "Terminal" : "Chat" });

/** Which record an editor names, and the ID prefix used when a name starts with a digit. */
const namedEditors: Partial<Record<Editor, { field: string; label: string; prefix: string }>> = {
	"agent-add": { field: "agent", label: "Agent name", prefix: "agent" },
	"role-add": { field: "role", label: "Role name", prefix: "role" },
	"mission-create": { field: "mission", label: "Mission title", prefix: "mission" },
	"task-assign": { field: "task", label: "Task title", prefix: "task" },
};

function MessageBody({ body }: { body: string }) {
	try {
		const parsed: unknown = JSON.parse(body);
		if (parsed && typeof parsed === "object") {
			return <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words font-mono text-xs text-muted-foreground">{JSON.stringify(parsed, null, 2)}</pre>;
		}
	} catch {
		// Plain text body.
	}
	return <p className="mt-2 whitespace-pre-wrap break-words text-sm">{body}</p>;
}

async function teamAction(projectId: string, operation: string, arguments_: Record<string, unknown> = {}, sessionId?: string) {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/team", {
		params: { path: { projectId } },
		body: { operation, arguments: arguments_, ...(sessionId ? { sessionId } : {}) },
	});
	if (error) throw new Error(apiErrorMessage(error));
	return data?.result;
}

function Evidence({ item }: { item: Mission | Task }) {
	return (
		<>
			{item.result && (
				<div className="mt-3">
					<p className="text-xs text-muted-foreground">Result</p>
					<p className="whitespace-pre-wrap text-sm">{item.result}</p>
				</div>
			)}
			{item.evidence && (
				<div className="mt-3">
					<p className="text-xs text-muted-foreground">Verification evidence</p>
					<p className="whitespace-pre-wrap text-sm">{item.evidence}</p>
				</div>
			)}
			{item.review && (
				<div className="mt-3">
					<p className="text-xs text-muted-foreground">Review or blocker</p>
					<p className="whitespace-pre-wrap text-sm">{item.review}</p>
				</div>
			)}
		</>
	);
}

export function TeamView() {
	const queryClient = useQueryClient();
	const [projectId, setProjectId] = useState("");
	const [staffingMission, setStaffingMission] = useState<Mission | null>(null);
	const [taskMission, setTaskMission] = useState("");
	const [tab, setTab] = useState<"missions" | "planning" | "agents" | "org">("missions");
	const [sheet, setSheet] = useState<"roles" | "log" | null>(null);
	const [title, setTitle] = useState("");
	const [editor, setEditor] = useState<Editor | null>(null);
	const [actionError, setActionError] = useState("");
	const [busy, setBusy] = useState(false);
	const [role, setRole] = useState("worker");
	const [provider, setProvider] = useState("codex");
	const [addingProject, setAddingProject] = useState(false);
	const [projectPath, setProjectPath] = useState("");
	const projects = useQuery({
		queryKey: ["team-projects"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects");
			if (error) throw new Error(apiErrorMessage(error));
			return data?.projects ?? [];
		},
	});
	useEffect(() => {
		if (!projectId && projects.data?.length) setProjectId(projects.data[0].id);
	}, [projectId, projects.data]);
	const team = useQuery({
		queryKey: ["project-team", projectId],
		enabled: Boolean(projectId),
		refetchInterval: 3000,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data?.result as TeamState;
		},
	});
	const state = team.data;
	async function addProject(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		setBusy(true);
		setActionError("");
		try {
			const { data, error } = await apiClient.POST("/api/v1/projects", {
				body: { path: projectPath.trim() },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (!data?.project) throw new Error("Project registration did not return a project");
			setProjectId(data.project.id);
			setEditor("enable");
			setAddingProject(false);
			setProjectPath("");
			await queryClient.invalidateQueries({ queryKey: ["team-projects"] });
			await queryClient.invalidateQueries({ queryKey: ["workspaces"] });
		} catch (error) {
			setActionError(error instanceof Error ? error.message : "Could not add project");
		} finally {
			setBusy(false);
		}
	}
	async function execute(operation: string, arguments_: Record<string, unknown>, sessionId?: string) {
		setBusy(true);
		setActionError("");
		try {
			await teamAction(projectId, operation, arguments_, sessionId);
			await queryClient.invalidateQueries({
				queryKey: ["project-team", projectId],
			});
			setEditor(null);
			return true;
		} catch (error) {
			setActionError(error instanceof Error ? error.message : "Team action failed");
			return false;
		} finally {
			setBusy(false);
		}
	}
	const roleTier = state?.roles.find((item) => item.id === role)?.tier ?? "worker";
	const selectedTaskMission = state?.missions.find((item) => item.id === taskMission && item.status === "active") ?? state?.missions.find((item) => item.status === "active");
	const fields: Record<Editor, Field[]> = {
		"agent-add": [
			{
				name: "provider",
				label: "Provider",
				options: ["codex", "muse", "claude-code"].map((value) => ({ value, label: providerLabel(value) })),
			},
			{
				name: "role",
				label: "Role",
				options: state?.roles.map((item) => item.id) ?? [],
			},
			...(["orchestrator", "planner"].includes(roleTier)
				? []
				: [
						{
							name: "parent",
							label: "Supervisor",
							options:
								state?.agents
									.filter((item) =>
									roleTier === "chief"
										? item.tier === "orchestrator"
										: item.tier === "chief" || item.tier === "orchestrator",
								)
									.sort((left, right) => left.tier.localeCompare(right.tier))
									.map((item) => item.id) ?? [],
						},
					]),
			{
				name: "mode",
				label: "Interface",
				options: (provider === "muse" ? ["tui"] : roleTier === "planner" ? ["chat", "tui"] : ["tui", "chat"]).map(modeOption),
			},
		],
		"role-add": [
			{
				name: "tier",
				label: "Level",
				options: ["worker", "chief", "orchestrator", "planner"].map((value) => ({ value, label: tierLabel(value) })),
			},
			{ name: "instructions", label: "Role instructions", multiline: true },
		],
		"mission-create": [
			{ name: "objective", label: "Objective", multiline: true },
			{ name: "criteria", label: "Acceptance criteria", multiline: true },
		],
		"task-assign": [
			{
				name: "mission",
				label: "Mission",
				options: state?.missions.filter((item) => item.status === "active").map((item) => item.id) ?? [],
			},
			{
				name: "worker",
				label: "Worker",
				options: selectedTaskMission?.workers ?? [],
			},
			{ name: "objective", label: "Task objective", multiline: true },
		],
		send: [
			{
				name: "recipient",
				label: "Recipient",
				options: state?.agents.map((item) => item.id) ?? [],
			},
			{ name: "subject", label: "Subject" },
			{ name: "body", label: "Message", multiline: true },
		],
		enable: [
			{
				name: "memory_root",
				label: "Datashard directory override (optional)",
				optional: true,
			},
		],
	};
	const titles: Record<Editor, string> = {
		"agent-add": "Spawn agent",
		"role-add": "Define custom role",
		"mission-create": "Define mission",
		"task-assign": "Assign worker task",
		send: "Send message",
		enable: "Enable project team",
	};
	const takenIds: Record<string, string[]> = {
		agent: state?.agents.map((item) => item.id) ?? [],
		role: state?.roles.map((item) => item.id) ?? [],
		mission: [...(state?.missions.map((item) => item.id) ?? []), ...(state?.drafts ?? []).map((item) => item.id)],
		task: state?.tasks.map((item) => item.id) ?? [],
	};
	const named = editor ? namedEditors[editor] : undefined;
	const generatedId = named ? uniqueId(title, takenIds[named.field], named.prefix) : "";
	function edit(operation: Editor) {
		setActionError("");
		setTitle("");
		setRole("worker");
		setProvider("codex");
		setEditor(operation);
	}
	function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		if (!editor) return;
		const arguments_: Record<string, unknown> = Object.fromEntries(new FormData(event.currentTarget));
		delete arguments_.title;
		if (named) {
			if (!generatedId) {
				setActionError(`${named.label} needs at least one letter or digit`);
				return;
			}
			arguments_[named.field] = generatedId;
		}
		if (editor === "mission-create") arguments_.workers = new FormData(event.currentTarget).getAll("workers");
		if (editor === "agent-add") {
			if (!arguments_.parent) arguments_.parent = null;
		}
		if (editor === "send") arguments_.key = crypto.randomUUID();
		if (editor === "enable" && !arguments_.memory_root) delete arguments_.memory_root;
		void execute(editor, arguments_);
	}
	const error =
		actionError ||
		(team.error instanceof Error ? team.error.message : "") ||
		(projects.error instanceof Error ? projects.error.message : "");
	const hasPlanner = Boolean(state?.agents.some((agent) => agent.tier === "planner"));
	const noProjects = !projects.isPending && !projects.data?.length;
	const anyDialog = Boolean(editor || staffingMission || (addingProject && !noProjects));
	const addProjectForm = (
		<form onSubmit={addProject} className="space-y-4">
			<label className="block text-sm">
				Project folder
				<Input required value={projectPath} onChange={(event) => setProjectPath(event.target.value)} className="mt-1" />
			</label>
			<div className="flex gap-2">
				{window.ao && (
					<Button
						type="button"
						variant="secondary"
						onClick={async () => {
							try {
								const path = await aoBridge.app.chooseDirectory("Choose a project repository");
								if (path) setProjectPath(path);
							} catch (error) {
								setActionError(error instanceof Error ? error.message : "Could not choose folder");
							}
						}}
					>
						Browse folders
					</Button>
				)}
				<Button type="submit" disabled={busy}>
					{busy ? "Adding project…" : "Add project folder"}
				</Button>
			</div>
		</form>
	);
	return (
		<div className="flex h-full min-h-0 flex-col">
			<PageTopbar lead={<PageTopbarTitle>Project teams</PageTopbarTitle>} />
			<div className="min-h-0 flex-1 overflow-auto p-6">
				<div className="mb-5 flex flex-wrap items-center gap-2">
					<label className="text-sm">
						Project{" "}
						<select
							aria-label="Team project"
							className="ml-2 h-8 rounded-md border bg-background px-2"
							value={projectId}
							onChange={(event) => {
								setProjectId(event.target.value);
								setStaffingMission(null);
								setEditor(null);
							}}
						>
							{projects.data?.map((project) => (
								<option key={project.id} value={project.id}>
									{project.name}
								</option>
							))}
						</select>
					</label>
					{state?.enabled && (
						<>
							<Button onClick={() => edit("agent-add")}>Spawn agent</Button>
							<Button
								variant="secondary"
								onClick={() => {
									setTab("planning");
									if (!hasPlanner) {
										edit("agent-add");
										setRole("planner");
									}
								}}
							>
								{hasPlanner ? "Work with planner" : "Create planner"}
							</Button>
						</>
					)}
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<Button variant="quiet" aria-label="More team actions">
								More
								<ChevronDown className="size-3.5" />
							</Button>
						</DropdownMenuTrigger>
						<DropdownMenuContent align="start">
							{state?.enabled && (
								<>
									<DropdownMenuItem onSelect={() => edit("mission-create")}>Define mission</DropdownMenuItem>
									<DropdownMenuItem onSelect={() => edit("role-add")}>Define role</DropdownMenuItem>
									<DropdownMenuItem onSelect={() => edit("send")}>Send message</DropdownMenuItem>
									<DropdownMenuSeparator />
									<DropdownMenuItem onSelect={() => setSheet("roles")}>Role definitions</DropdownMenuItem>
									<DropdownMenuItem onSelect={() => setSheet("log")}>Message log</DropdownMenuItem>
									<DropdownMenuSeparator />
								</>
							)}
							<DropdownMenuItem
								onSelect={() => {
									setAddingProject(true);
									setActionError("");
								}}
							>
								Add project
							</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				</div>
				{error && !anyDialog && (
					<p role="alert" className="mb-4 text-sm text-destructive">
						{error}
					</p>
				)}
				{noProjects && (
					<section className="mb-5 max-w-2xl">
						<h2 className="mb-3 font-medium">Add a local project</h2>
						{addProjectForm}
					</section>
				)}
				<TeamDialog
					open={addingProject && !noProjects}
					title="Add a local project"
					description="Choose a Git repository with at least one commit."
					error={actionError}
					onClose={() => setAddingProject(false)}
				>
					{addProjectForm}
				</TeamDialog>
				{projectId && state && !state.enabled && !editor && (
					<section className="max-w-2xl">
						<h2 className="mb-2 font-medium">Create a project team</h2>
						<p className="mb-4 text-sm text-muted-foreground">
							Enabling connects this project to llmwiki memory. You can then add a planner, an orchestrator, chiefs and
							workers.
						</p>
						<Button onClick={() => edit("enable")}>Enable team</Button>
					</section>
				)}
				<TeamDialog open={editor !== null} title={editor ? titles[editor] : ""} error={actionError} onClose={() => setEditor(null)}>
					{editor && (
						<form key={editor + projectId} onSubmit={submit} className="space-y-4">
							{named && (
								<NameField label={named.label} value={title} id={generatedId} onChange={setTitle} />
							)}
							{editor === "mission-create" && <MissionStaffingFields agents={state?.agents ?? []} />}
							{editor === "enable" && (
								<p className="text-sm text-muted-foreground">
									Uses <code>LLMWIKI_MEMORY_ROOT</code> by default, or llmwiki's standard location when it is unset.
								</p>
							)}
							{fields[editor].map((field) => (
								<label key={field.name} className="block text-sm">
									{field.label}
									{field.options ? (
										<select
											name={field.name}
											required
											className="mt-1 block h-8 w-full rounded-md border bg-background px-2"
											key={field.name === "mode" ? provider + roleTier : field.name === "worker" ? selectedTaskMission?.id : field.name}
											value={
												editor === "task-assign" && field.name === "mission" ? selectedTaskMission?.id : editor === "agent-add"
													? field.name === "role"
														? role
														: field.name === "provider"
															? provider
															: undefined
													: undefined
											}
											onChange={
												field.name === "mission" && editor === "task-assign" ? (event) => setTaskMission(event.target.value) : field.name === "role" && editor === "agent-add"
													? (event) => setRole(event.target.value)
													: field.name === "provider" && editor === "agent-add"
														? (event) => setProvider(event.target.value)
														: undefined
											}
										>
											{field.options.map((option) => (
												<option key={optionValue(option)} value={optionValue(option)}>
													{optionLabel(option)}
												</option>
											))}
										</select>
									) : field.multiline ? (
										<textarea
											name={field.name}
											required
											className="mt-1 block min-h-24 w-full rounded-md border bg-background p-2"
										/>
									) : (
										<Input name={field.name} required={!field.optional} className="mt-1" />
									)}
								</label>
							))}
							<div className="flex justify-end gap-2">
								<Button type="button" variant="ghost" disabled={busy} onClick={() => setEditor(null)}>
									Cancel
								</Button>
								<Button type="submit" disabled={busy}>
									{busy ? "Saving…" : "Save"}
								</Button>
							</div>
						</form>
					)}
				</TeamDialog>
				{state?.enabled && (
					<>
						<TeamDialog
							open={staffingMission !== null}
							title={staffingMission ? `Staff mission ${staffingMission.id}` : ""}
							error={actionError}
							onClose={() => setStaffingMission(null)}
						>
							{staffingMission && (
								<form
									className="space-y-4"
									onSubmit={async (event) => {
										event.preventDefault();
										const workers = new FormData(event.currentTarget).getAll("workers");
										if (await execute("mission-staff", { mission: staffingMission.id, revision: staffingMission.revision, workers })) setStaffingMission(null);
									}}
								>
									<MissionStaffingFields key={staffingMission.id} agents={state.agents} supervisor={staffingMission.supervisor} workers={staffingMission.workers} fixedSupervisor />
									<div className="flex justify-end gap-2">
										<Button type="button" variant="ghost" onClick={() => setStaffingMission(null)}>Cancel</Button>
										<Button disabled={busy}>Save staffing</Button>
									</div>
								</form>
							)}
						</TeamDialog>
						<Sheet open={sheet !== null} onOpenChange={(open) => !open && setSheet(null)}>
							<SheetContent className="w-full overflow-y-auto sm:max-w-lg">
								<SheetHeader>
									<SheetTitle>{sheet === "roles" ? "Role definitions" : "Message log"}</SheetTitle>
									<SheetDescription className="sr-only">
										{sheet === "roles" ? "Standing instructions for each team role" : "Every message sent between team agents"}
									</SheetDescription>
								</SheetHeader>
								<div className="divide-y px-4 pb-4">
									{sheet === "roles" &&
										state.roles.map((item) => (
											<section key={item.id} className="py-3">
												<h3 className="text-sm font-medium">
													{item.id} <span className="text-xs text-muted-foreground">{tierLabel(item.tier)}</span>
												</h3>
												<p className="mt-1 whitespace-pre-wrap text-sm text-muted-foreground">{item.instructions}</p>
											</section>
										))}
									{sheet === "log" && !state.messages.length && (
										<p className="py-3 text-sm text-muted-foreground">No team messages yet.</p>
									)}
									{sheet === "log" &&
										state.messages.map((message) => (
											<article key={message.id} className="py-3">
												<div className="flex justify-between gap-3">
													<h3 className="text-sm font-medium">{message.subject}</h3>
													<span className="shrink-0 text-xs text-muted-foreground">
														{message.acknowledged_at
															? "Handled"
															: message.delivered_at
																? "Delivered · pending action"
																: "Not yet read"}
													</span>
												</div>
												<p className="mt-1 text-xs text-muted-foreground">
													{message.sender} → {message.recipient} · {new Date(message.created_at).toLocaleString()}
												</p>
												<MessageBody body={message.body} />
											</article>
										))}
								</div>
							</SheetContent>
						</Sheet>
						<div className="mb-5 flex flex-wrap gap-1" role="tablist" aria-label="Project team">
							{(["missions", "planning", "agents", "org"] as const).map((item) => (
								<Button
									key={item}
									role="tab"
									aria-selected={tab === item}
									data-active={tab === item ? "true" : undefined}
									variant="ghost"
									size="sm"
									onClick={() => setTab(item)}
								>
									{{ org: "Org chart", planning: "Planning", agents: "Agents", missions: "Missions" }[item]}
								</Button>
							))}
						</div>
						{tab === "org" && (
							<TeamOrgChart key={projectId} projectId={projectId} state={state} busy={busy} error={actionError} onAction={execute} />
						)}
						{tab === "planning" && (
							<TeamPlanning key={projectId} projectId={projectId} state={state} busy={busy} error={actionError} onAction={execute} />
						)}
						{tab === "agents" && (
							<>
								{!state.agents.length && (
									<p className="text-sm text-muted-foreground">No agents yet.</p>
								)}
								<div className="divide-y">
									{state.agents.map((agent) => (
										<article key={agent.id} aria-label={agent.id} className="py-3">
											<div className="flex items-baseline justify-between gap-3">
												<h2 className="font-medium">{agent.id}</h2>
												<span className="text-xs text-muted-foreground">
													{agent.enabled ? (agent.session_id ? "Running" : "Starting") : "Paused"}
												</span>
											</div>
											<p className="mt-1 text-sm text-muted-foreground">
												{[agent.role === agent.tier ? tierLabel(agent.tier) : `${agent.role} (${tierLabel(agent.tier)})`, providerLabel(agent.provider), agent.parent ? `Reports to ${agent.parent}` : null]
													.filter(Boolean)
													.join(" · ")}
											</p>
											{state.tasks
												.filter((task) => task.worker === agent.id && task.status !== "accepted")
												.map((task) => (
													<p key={task.id} className="mt-1 text-sm">
														Task {task.id} · {task.mission} · {task.status}
													</p>
												))}
											{(state.pending[agent.id] ?? 0) > 0 && (
												<p className="mt-1 text-sm">{state.pending[agent.id]} unread {state.pending[agent.id] === 1 ? "message" : "messages"}</p>
											)}
											{agent.launch_error && <p className="mt-1 text-sm text-destructive">{agent.launch_error}</p>}
											<div className="mt-2 flex flex-wrap gap-1">
												{agent.session_id && (
													<Button asChild variant="secondary" size="sm">
														<Link
															to="/projects/$projectId/sessions/$sessionId"
															params={{
																projectId,
																sessionId: agent.session_id,
															}}
														>
															Open session
														</Link>
													</Button>
												)}
												<Button
													size="sm"
													variant="quiet"
													disabled={busy}
													onClick={() =>
														void execute("agent-control", {
															agent: agent.id,
															action: agent.enabled ? "pause" : "resume",
														})
													}
												>
													{agent.enabled ? "Pause" : "Resume"}
												</Button>
												{agent.session_id && agent.enabled ? (
													<Button
														size="sm"
														variant="destructive-ghost"
														disabled={busy}
														onClick={() =>
															void execute("agent-control", {
																agent: agent.id,
																action: "cancel",
															})
														}
													>
														Stop
													</Button>
												) : null}
											</div>
										</article>
									))}
								</div>
							</>
						)}
						{tab === "missions" && (
							<>
								<div className="mb-4">
									<Button
										variant="secondary"
										disabled={!state.missions.some((item) => item.status === "active")}
										onClick={() => edit("task-assign")}
									>
										Assign worker task
									</Button>
								</div>
								{!state.missions.length && (
									<p className="text-sm text-muted-foreground">No missions yet. Plan one with your planner.</p>
								)}
								<div className="space-y-4">
									{state.missions.map((mission) => {
										const missionTasks = state.tasks.filter((task) => task.mission === mission.id);
										const principalName = state.principal?.name ?? "Principal";
										const tasksAccepted = missionTasks.every((task) => task.status === "accepted");
										return (
										<article key={mission.id} className="rounded-lg border p-5">
											<div className="flex justify-between gap-4">
												<h2 className="font-medium">{mission.id}</h2>
												<span className="text-sm">{mission.status}</span>
											</div>
											<p className="mt-1 text-xs text-muted-foreground">
												{mission.supervisor === mission.orchestrator
													? `Directly supervised by ${mission.supervisor}`
													: `Supervisor ${mission.supervisor} · Final signoff ${mission.orchestrator}`}
											</p>
											<p className="mt-3 whitespace-pre-wrap text-sm">{mission.objective}</p>
											<p className="mt-3 text-sm">Assigned workers: {mission.workers?.join(", ") || "Unassigned — set staffing before assigning tasks"}</p>
											{["active", "blocked"].includes(mission.status) && <Button variant="secondary" size="sm" className="mt-3" onClick={() => { setStaffingMission(mission); setActionError(""); }}>Edit staffing for {mission.id}</Button>}
											<p className="mt-3 text-xs text-muted-foreground">Acceptance criteria</p>
											<p className="whitespace-pre-wrap text-sm">{mission.criteria}</p>
											{mission.plan && (
												<details className="mt-3 text-sm">
													<summary>Approved execution plan</summary>
													<p className="mt-2 whitespace-pre-wrap">{mission.plan}</p>
												</details>
											)}
											<Evidence item={mission} />
											{mission.status === "active" && tasksAccepted && (
												<MissionSubmitForm
													missionId={mission.id}
													reviewer={mission.supervisor}
													principalName={principalName}
													busy={busy}
													onSubmit={(result, evidence) =>
														void execute("submit", { kind: "mission", record: mission.id, revision: mission.revision, result, evidence })
													}
												/>
											)}
											{mission.status === "review" && (
												<ReviewForm
													kind="mission"
													recordId={mission.id}
													reviewer={mission.orchestrator}
													principalName={principalName}
													acceptLabel="Approve"
													busy={busy}
													onReview={(approve, feedback) =>
														void execute("review", { kind: "mission", record: mission.id, revision: mission.revision, approve, feedback })
													}
												/>
											)}
											<div className="mt-4 space-y-3">
												{missionTasks
													.map((task) => (
														<div key={task.id} className="rounded-md bg-muted/40 p-3">
															<div className="flex justify-between gap-3">
																<h3 className="text-sm font-medium">
																	{task.id} · {task.worker}
																</h3>
																<span className="text-xs">{task.status}</span>
															</div>
															<p className="mt-2 whitespace-pre-wrap text-sm">{task.objective}</p>
															<Evidence item={task} />
															{task.status === "review" && (
																<ReviewForm
																	kind="task"
																	recordId={task.id}
																	reviewer={mission.supervisor}
																	principalName={principalName}
																	acceptLabel="Accept"
																	busy={busy}
																	onReview={(approve, feedback) =>
																		void execute("review", { kind: "task", record: task.id, revision: task.revision, approve, feedback })
																	}
																/>
															)}
														</div>
													))}
											</div>
										</article>
										);
									})}
								</div>
							</>
						)}
					</>
				)}
			</div>
		</div>
	);
}
