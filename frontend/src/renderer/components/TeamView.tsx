import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { PageTopbar, PageTopbarTitle } from "./PageTopbar";
import { TeamPlanning } from "./TeamPlanning";
import { TeamOrgChart } from "./TeamOrgChart";

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
	id: string;
	chief: string;
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
};
export type MissionDraft = {
	id: string;
	planner: string;
	chief: string | null;
	objective: string;
	criteria: string;
	plan: string;
	status: string;
	revision: number;
	mission: string | null;
};
export type TeamAction = (operation: string, arguments_: Record<string, unknown>) => Promise<boolean>;
type Editor = "agent-add" | "role-add" | "mission-create" | "task-assign" | "send" | "enable";
type Field = {
	name: string;
	label: string;
	options?: string[];
	multiline?: boolean;
	optional?: boolean;
	value?: string;
};

async function teamAction(projectId: string, operation: string, arguments_: Record<string, unknown> = {}) {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/team", {
		params: { path: { projectId } },
		body: { operation, arguments: arguments_ },
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
	const [tab, setTab] = useState<"org" | "planning" | "agents" | "missions" | "mail">("org");
	const [editor, setEditor] = useState<Editor | null>(null);
	const [actionError, setActionError] = useState("");
	const [busy, setBusy] = useState(false);
	const [role, setRole] = useState("worker");
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
	async function execute(operation: string, arguments_: Record<string, unknown>) {
		setBusy(true);
		setActionError("");
		try {
			await teamAction(projectId, operation, arguments_);
			await queryClient.invalidateQueries({ queryKey: ["project-team", projectId] });
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
	const fields: Record<Editor, Field[]> = {
		"agent-add": [
			{ name: "agent", label: "Agent ID" },
			{ name: "role", label: "Role", options: state?.roles.map((item) => item.id) ?? [] },
			...(["orchestrator", "planner"].includes(roleTier)
				? []
				: [
						{
							name: "parent",
							label: "Supervisor",
							options:
								state?.agents
									.filter((item) => item.tier === (roleTier === "chief" ? "orchestrator" : "chief"))
									.map((item) => item.id) ?? [],
						},
					]),
			{ name: "mode", label: "Agent interface", options: roleTier === "planner" ? ["chat", "tui"] : ["tui", "chat"] },
		],
		"role-add": [
			{ name: "role", label: "Role ID" },
			{ name: "tier", label: "Hierarchy tier", options: ["worker", "chief", "orchestrator", "planner"] },
			{ name: "instructions", label: "Role instructions", multiline: true },
		],
		"mission-create": [
			{ name: "mission", label: "Mission ID" },
			{
				name: "chief",
				label: "Responsible chief",
				options: state?.agents.filter((item) => item.tier === "chief").map((item) => item.id) ?? [],
			},
			{ name: "objective", label: "Objective", multiline: true },
			{ name: "criteria", label: "Acceptance criteria", multiline: true },
		],
		"task-assign": [
			{ name: "task", label: "Task ID" },
			{
				name: "mission",
				label: "Mission",
				options: state?.missions.filter((item) => item.status === "active").map((item) => item.id) ?? [],
			},
			{
				name: "worker",
				label: "Worker",
				options: state?.agents.filter((item) => item.tier === "worker").map((item) => item.id) ?? [],
			},
			{ name: "objective", label: "Task objective", multiline: true },
		],
		send: [
			{ name: "recipient", label: "Recipient", options: state?.agents.map((item) => item.id) ?? [] },
			{ name: "subject", label: "Subject" },
			{ name: "body", label: "Message", multiline: true },
		],
		enable: [
			{ name: "memory_root", label: "Datashard directory (optional, uses the configured memory root)", optional: true },
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
	function edit(operation: Editor) {
		setActionError("");
		setRole("worker");
		setEditor(operation);
	}
	function submit(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		if (!editor) return;
		const arguments_: Record<string, unknown> = Object.fromEntries(new FormData(event.currentTarget));
		if (editor === "agent-add") {
			arguments_.provider = "codex";
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
	return (
		<div className="flex h-full min-h-0 flex-col">
			<PageTopbar lead={<PageTopbarTitle>Project teams</PageTopbarTitle>} />
			<div className="min-h-0 flex-1 overflow-auto p-6">
				<div className="mb-5 flex flex-wrap items-center gap-3">
					<label className="text-sm">
						Project{" "}
						<select
							aria-label="Team project"
							className="ml-2 rounded-md border bg-background px-3 py-2"
							value={projectId}
							onChange={(event) => {
								setProjectId(event.target.value);
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
								variant="outline"
								onClick={() => {
									setTab("planning");
									if (!state.agents.some((agent) => agent.tier === "planner")) {
										edit("agent-add");
										setRole("planner");
									}
								}}
							>
								{state.agents.some((agent) => agent.tier === "planner") ? "Work with planner" : "Create planner"}
							</Button>
							<Button variant="outline" onClick={() => edit("mission-create")}>
								Define mission
							</Button>
							<Button variant="outline" onClick={() => edit("role-add")}>
								Define role
							</Button>
							<Button variant="outline" onClick={() => edit("send")}>
								Send message
							</Button>
						</>
					)}
				</div>
				{error && (
					<p role="alert" className="mb-4 rounded-md border border-destructive p-3 text-sm text-destructive">
						{error}
					</p>
				)}
				{!projects.isPending && !projects.data?.length && (
					<p className="text-sm text-muted-foreground">Add a local project to create its team.</p>
				)}
				{projectId && state && !state.enabled && !editor && (
					<section className="max-w-2xl rounded-lg border p-6">
						<h2 className="mb-2 font-medium">Create a project team</h2>
						<p className="mb-4 text-sm text-muted-foreground">
							Connect this project to llmwiki/datashard, then spawn an orchestrator, mission chiefs, and workers.
							Enabling creates a memory binding when the project needs one.
						</p>
						<Button onClick={() => edit("enable")}>Enable team</Button>
					</section>
				)}
				{editor && (
					<form key={editor + projectId} onSubmit={submit} className="mb-6 max-w-2xl rounded-lg border p-5">
						<h2 className="mb-4 font-medium">{titles[editor]}</h2>
						<div className="space-y-4">
							{fields[editor].map((field) => (
								<label key={field.name} className="block text-sm">
									{field.label}
									{field.options ? (
										<select
											name={field.name}
											required
											className="mt-1 block w-full rounded-md border bg-background p-2"
											value={field.name === "role" && editor === "agent-add" ? role : undefined}
											onChange={
												field.name === "role" && editor === "agent-add"
													? (event) => setRole(event.target.value)
													: undefined
											}
										>
											{field.options.map((value) => (
												<option key={value} value={value}>
													{value}
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
						</div>
						<div className="mt-5 flex gap-2">
							<Button type="submit" disabled={busy}>
								{busy ? "Saving…" : "Save"}
							</Button>
							<Button type="button" variant="ghost" disabled={busy} onClick={() => setEditor(null)}>
								Cancel
							</Button>
						</div>
					</form>
				)}
				{state?.enabled && (
					<>
						<div className="mb-5 flex flex-wrap gap-2" role="tablist" aria-label="Project team">
							{(["org", "planning", "agents", "missions", "mail"] as const).map((item) => (
								<Button
									key={item}
									role="tab"
									aria-selected={tab === item}
									variant={tab === item ? "secondary" : "ghost"}
									onClick={() => setTab(item)}
								>
									{
										{ org: "Org chart", planning: "Planning", agents: "Agents", missions: "Missions", mail: "Mailbox" }[
											item
										]
									}
								</Button>
							))}
						</div>
						{tab === "org" && (
							<TeamOrgChart key={projectId} projectId={projectId} state={state} busy={busy} onAction={execute} />
						)}
						{tab === "planning" && (
							<TeamPlanning key={projectId} projectId={projectId} state={state} busy={busy} onAction={execute} />
						)}
						{tab === "agents" && (
							<>
								{!state.agents.length && (
									<p className="text-sm text-muted-foreground">
										Spawn an orchestrator, then chiefs and workers under their supervisors.
									</p>
								)}
								<div className="grid gap-4 lg:grid-cols-2 xl:grid-cols-3">
									{state.agents.map((agent) => (
										<article key={agent.id} className="rounded-lg border p-4">
											<div className="flex justify-between gap-3">
												<h2 className="font-medium">{agent.id}</h2>
												<span className="text-xs text-muted-foreground">
													{agent.enabled ? (agent.session_id ? "Attached" : "Launch queued") : "Paused"}
												</span>
											</div>
											<p className="mt-2 text-sm text-muted-foreground">
												{agent.tier} · {agent.role} · Codex
											</p>
											<p className="mt-1 text-sm">Supervisor {agent.parent ?? "project"}</p>
											<p className="mt-2 text-sm">{state.pending[agent.id] ?? 0} pending messages</p>
											{agent.launch_error && <p className="mt-2 text-sm text-destructive">{agent.launch_error}</p>}
											<div className="mt-4 flex flex-wrap gap-2">
												{agent.session_id && (
													<Button asChild variant="outline" size="sm">
														<Link
															to="/projects/$projectId/sessions/$sessionId"
															params={{ projectId, sessionId: agent.session_id }}
														>
															Open session
														</Link>
													</Button>
												)}
												<Button
													size="sm"
													variant="ghost"
													disabled={busy}
													onClick={() =>
														void execute("agent-control", {
															agent: agent.id,
															action: agent.enabled ? "pause" : "resume",
														})
													}
												>
													{agent.enabled ? "Pause dispatch" : "Resume"}
												</Button>
												{agent.session_id && agent.enabled ? (
													<Button
														size="sm"
														variant="ghost"
														disabled={busy}
														onClick={() => void execute("agent-control", { agent: agent.id, action: "cancel" })}
													>
														Stop agent
													</Button>
												) : null}
											</div>
										</article>
									))}
								</div>
								<h2 className="mb-3 mt-7 font-medium">Role definitions</h2>
								<div className="grid gap-3 lg:grid-cols-2">
									{state.roles.map((item) => (
										<article key={item.id} className="rounded-lg border p-4">
											<h3 className="font-medium">
												{item.id} <span className="text-xs text-muted-foreground">{item.tier}</span>
											</h3>
											<p className="mt-2 whitespace-pre-wrap text-sm text-muted-foreground">{item.instructions}</p>
										</article>
									))}
								</div>
							</>
						)}
						{tab === "missions" && (
							<>
								<div className="mb-4">
									<Button
										variant="outline"
										disabled={!state.missions.some((item) => item.status === "active")}
										onClick={() => edit("task-assign")}
									>
										Assign worker task
									</Button>
								</div>
								{!state.missions.length && (
									<p className="text-sm text-muted-foreground">Define a mission and acceptance criteria for a chief.</p>
								)}
								<div className="space-y-4">
									{state.missions.map((mission) => (
										<article key={mission.id} className="rounded-lg border p-5">
											<div className="flex justify-between gap-4">
												<h2 className="font-medium">{mission.id}</h2>
												<span className="text-sm">{mission.status}</span>
											</div>
											<p className="mt-1 text-xs text-muted-foreground">
												Chief {mission.chief} · Final signoff {mission.orchestrator} · Revision {mission.revision}
											</p>
											<p className="mt-3 whitespace-pre-wrap text-sm">{mission.objective}</p>
											<p className="mt-3 text-xs text-muted-foreground">Acceptance criteria</p>
											<p className="whitespace-pre-wrap text-sm">{mission.criteria}</p>
											{mission.plan && (
												<details className="mt-3 text-sm">
													<summary>Approved execution plan</summary>
													<p className="mt-2 whitespace-pre-wrap">{mission.plan}</p>
												</details>
											)}
											<Evidence item={mission} />
											<div className="mt-4 space-y-3">
												{state.tasks
													.filter((task) => task.mission === mission.id)
													.map((task) => (
														<div key={task.id} className="rounded-md bg-muted/40 p-3">
															<div className="flex justify-between gap-3">
																<h3 className="text-sm font-medium">
																	{task.id} · {task.worker}
																</h3>
																<span className="text-xs">
																	{task.status} · Revision {task.revision}
																</span>
															</div>
															<p className="mt-2 whitespace-pre-wrap text-sm">{task.objective}</p>
															<Evidence item={task} />
														</div>
													))}
											</div>
										</article>
									))}
								</div>
							</>
						)}
						{tab === "mail" && (
							<div className="space-y-3">
								{!state.messages.length && (
									<p className="text-sm text-muted-foreground">Team communication will appear here.</p>
								)}
								{state.messages.map((message) => (
									<article key={message.id} className="rounded-lg border p-4">
										<div className="flex justify-between gap-3">
											<h2 className="font-medium">{message.subject}</h2>
											<span className="text-xs text-muted-foreground">
												{message.acknowledged_at
													? "Handled"
													: message.delivered_at
														? "Delivered · pending action"
														: "Pending delivery"}
											</span>
										</div>
										<p className="mt-1 text-xs text-muted-foreground">
											{message.sender} → {message.recipient} · {new Date(message.created_at).toLocaleString()}
										</p>
										<p className="mt-3 whitespace-pre-wrap break-words text-sm">{message.body}</p>
									</article>
								))}
							</div>
						)}
					</>
				)}
			</div>
		</div>
	);
}
