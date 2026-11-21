import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { TeamView, type TeamState } from "./TeamView";

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.get, POST: mocks.post },
	apiErrorMessage: (error: { message: string }) => error.message,
}));
vi.mock("@tanstack/react-router", () => ({
	Link: ({ children, params }: { children: ReactNode; params?: { projectId: string; sessionId: string } }) => (
		<a href={params ? `/projects/${params.projectId}/sessions/${params.sessionId}` : "#"}>{children}</a>
	),
}));
vi.mock("./PageTopbar", () => ({
	PageTopbar: ({ lead }: { lead: ReactNode }) => <header>{lead}</header>,
	PageTopbarTitle: ({ children }: { children: ReactNode }) => <h1>{children}</h1>,
}));

const initial: TeamState = {
	enabled: true,
	roles: [
		{
			id: "orchestrator",
			tier: "orchestrator",
			instructions: "Sign off missions",
		},
		{ id: "chief", tier: "chief", instructions: "Drive workers" },
		{ id: "worker", tier: "worker", instructions: "Implement tasks" },
		{
			id: "planner",
			tier: "planner",
			instructions: "Plan missions with the user",
		},
	],
	agents: [
		{
			id: "orch",
			tier: "orchestrator",
			role: "orchestrator",
			parent: null,
			provider: "codex",
			enabled: 1,
			session_id: null,
			launch_error: null,
			revision: 1,
		},
		{
			id: "chief-one",
			tier: "chief",
			role: "chief",
			parent: "orch",
			provider: "codex",
			enabled: 1,
			session_id: null,
			launch_error: null,
			revision: 1,
		},
	],
	missions: [],
	drafts: [],
	tasks: [],
	messages: [],
	pending: {},
};

function mount() {
	const client = new QueryClient({
		defaultOptions: { queries: { retry: false } },
	});
	render(
		<QueryClientProvider client={client}>
			<TeamView />
		</QueryClientProvider>,
	);
}

function serve(state: TeamState) {
	mocks.get.mockImplementation(async (path: string) =>
		path === "/api/v1/projects"
			? { data: { projects: [{ id: "project", name: "My project" }] } }
			: { data: { result: structuredClone(state) } },
	);
}

function plannedState(): TeamState {
	const state = structuredClone(initial);
	state.agents.push({
		...state.agents[0],
		id: "project-planner",
		tier: "planner",
		role: "planner",
		session_id: "project-4",
	});
	state.drafts.push({
		id: "csv-export",
		planner: "project-planner",
		chief: "chief-one",
		supervisor: "chief-one",
		workers: ["worker-one"],
		objective: "Export filtered reports",
		criteria: "Filters preserved",
		plan: "Inspect, implement, test",
		status: "draft",
		revision: 7,
		mission: null,
	});
	return state;
}

beforeEach(() => {
	mocks.get.mockReset();
	mocks.post.mockReset();
	mocks.get.mockImplementation(async (path: string) =>
		path === "/api/v1/projects"
			? { data: { projects: [{ id: "project", name: "My project" }] } }
			: { data: { result: structuredClone(initial) } },
	);
	mocks.post.mockResolvedValue({ data: { result: {} } });
});
afterEach(cleanup);

describe("project teams", () => {
	it("saves the selected supervisor and named workers in a mission spec", async () => {
		const state = structuredClone(initial);
		for (const id of ["worker-b", "worker-c"]) state.agents.push({ ...state.agents[1], id, tier: "worker", role: "worker", parent: "chief-one" });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Define mission" }));
		fireEvent.change(screen.getByLabelText("Responsible supervisor"), { target: { value: "chief-one" } });
		fireEvent.change(screen.getByLabelText("Mission ID"), { target: { value: "staffed-export" } });
		fireEvent.change(screen.getByLabelText("Objective"), { target: { value: "Export reports" } });
		fireEvent.change(screen.getByLabelText("Acceptance criteria"), { target: { value: "Verified" } });
		fireEvent.click(screen.getByRole("checkbox", { name: /worker-b/ }));
		fireEvent.click(screen.getByRole("checkbox", { name: /worker-c/ }));
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
			params: { path: { projectId: "project" } }, body: { operation: "mission-create", arguments: {
				mission: "staffed-export", supervisor: "chief-one", objective: "Export reports", criteria: "Verified", workers: ["worker-b", "worker-c"],
			} },
		}));
	});

	it("requires a worker roster before releasing a complete planning draft", async () => {
		const state = plannedState(); state.drafts[0].workers = [];
		serve(state); mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Planning" }));
		expect(screen.getByRole("button", { name: "Release mission csv-export" })).toBeDisabled();
		expect(screen.getByText("Assigned workers: Unassigned")).toBeInTheDocument();
	});

	it("offers only the selected mission's staffed workers when assigning a task", async () => {
		const state = structuredClone(initial);
		for (const [id, worker] of [["first", "worker-b"], ["second", "worker-c"]]) state.missions.push({ id, chief: "chief-one", supervisor: "chief-one", workers: [worker], orchestrator: "orch", objective: "Deliver", criteria: "Verify", status: "active", revision: 1, result: null, evidence: null, review: null });
		serve(state); mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.click(screen.getByRole("button", { name: "Assign worker task" }));
		expect(screen.getByLabelText("Worker")).toHaveValue("worker-b");
		fireEvent.change(screen.getByLabelText("Mission"), { target: { value: "second" } });
		expect(screen.getByLabelText("Worker")).toHaveValue("worker-c");
		expect(within(screen.getByLabelText("Worker")).queryByRole("option", { name: "worker-b" })).not.toBeInTheDocument();
	});

	it("revises a mission roster with its current revision and preserves a conflicting edit", async () => {
		const state = plannedState();
		for (const id of ["worker-b", "worker-c"]) state.agents.push({ ...state.agents[1], id, tier: "worker", role: "worker", parent: "chief-one" });
		state.missions.push({ id: "staffed", chief: "chief-one", supervisor: "chief-one", workers: ["worker-b"], orchestrator: "orch", objective: "Deliver", criteria: "Verified", status: "active", revision: 4, result: null, evidence: null, review: null });
		serve(state); mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.click(screen.getByRole("button", { name: "Edit staffing for staffed" }));
		fireEvent.click(screen.getByRole("checkbox", { name: /worker-c/ }));
		mocks.post.mockResolvedValue({ error: { message: "Mission changed" } });
		fireEvent.click(screen.getByRole("button", { name: "Save staffing" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Mission changed");
		expect(screen.getByRole("checkbox", { name: /worker-c/ })).toBeChecked();
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
			params: { path: { projectId: "project" } }, body: { operation: "mission-staff", arguments: { mission: "staffed", revision: 4, workers: ["worker-b", "worker-c"] } },
		});
	});
	it("spawns Muse workers through the terminal interface", async () => {
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Spawn agent" }));
		fireEvent.change(screen.getByLabelText("Agent provider"), {
			target: { value: "muse" },
		});
		expect(screen.getByLabelText("Agent interface")).toHaveValue("tui");
		expect(
			within(screen.getByLabelText("Agent interface")).queryByRole("option", {
				name: "chat",
			}),
		).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("Agent ID"), {
			target: { value: "muse-worker" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: {
						agent: "muse-worker",
						role: "worker",
						parent: "chief-one",
						provider: "muse",
						mode: "tui",
					},
				},
			}),
		);
	});
	it("registers a project from a typed folder and opens team setup", async () => {
		mocks.get.mockImplementation(async (path: string) =>
			path === "/api/v1/projects"
				? { data: { projects: [] } }
				: { data: { result: { ...structuredClone(initial), enabled: false } } },
		);
		mocks.post.mockResolvedValue({ data: { project: { id: "new-project" } } });
		mount();
		fireEvent.change(await screen.findByLabelText("Project folder"), {
			target: { value: "C:\\projects\\example" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Add project folder" }));
		expect(await screen.findByRole("heading", { name: "Enable project team" })).toBeInTheDocument();
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects", {
			body: { path: "C:\\projects\\example" },
		});
		expect(screen.getByText("LLMWIKI_MEMORY_ROOT")).toBeInTheDocument();
		expect(screen.getByText("2. Build your org chart")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "new-project" } },
				body: { operation: "enable", arguments: {} },
			}),
		);
	});

	it("keeps the project folder and shows registration failures", async () => {
		mocks.get.mockResolvedValue({ data: { projects: [] } });
		mocks.post.mockResolvedValue({
			error: { message: "Git repository needs an initial commit" },
		});
		mount();
		fireEvent.change(await screen.findByLabelText("Project folder"), {
			target: { value: "C:\\projects\\empty" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Add project folder" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Git repository needs an initial commit");
		expect(screen.getByLabelText("Project folder")).toHaveValue("C:\\projects\\empty");
	});
	it("shows workers, assigned missions, and their session controls in a dedicated view", async () => {
		const state = structuredClone(initial);
		state.agents.push({
			...state.agents[1],
			id: "worker-one",
			tier: "worker",
			role: "worker",
			parent: "chief-one",
			session_id: "project-5",
		});
		state.tasks.push({
			id: "verify-export",
			mission: "export",
			worker: "worker-one",
			objective: "Verify report exports",
			status: "review",
			revision: 2,
			result: "Verified",
			evidence: "Tests pass",
			review: null,
		});
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Workers" }));
		expect(screen.getByRole("heading", { name: "worker-one" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "orch" })).not.toBeInTheDocument();
		expect(screen.getByText("Task verify-export / mission export / review")).toBeInTheDocument();
		expect(screen.getByRole("link", { name: "Open session" })).toHaveAttribute(
			"href",
			"/projects/project/sessions/project-5",
		);
		expect(screen.getByRole("button", { name: "Stop agent" })).toBeInTheDocument();
	});
	it("creates a project planner in chat mode without a supervisor", async () => {
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Create planner" }));
		expect(screen.queryByLabelText("Supervisor")).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("Agent ID"), {
			target: { value: "planner-one" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: {
						agent: "planner-one",
						role: "planner",
						parent: null,
						mode: "chat",
						provider: "codex",
					},
				},
			}),
		);
	});

	it("opens the planner workspace and saves an initial planning request", async () => {
		serve(plannedState());
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Work with planner" }));
		expect(screen.getByRole("link", { name: "Open planner conversation" })).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Request mission planning" }));
		fireEvent.change(screen.getByLabelText("Draft ID"), {
			target: { value: "new-goal" },
		});
		fireEvent.change(screen.getByLabelText("Mission objective"), {
			target: { value: "Explore export requirements" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "draft-create",
					arguments: {
						draft: "new-goal",
						objective: "Explore export requirements",
						criteria: "",
						plan: "",
						supervisor: null,
						workers: [],
					},
				},
			}),
		);
	});

	it("releases only the explicitly reviewed draft revision", async () => {
		serve(plannedState());
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Planning" }));
		fireEvent.click(screen.getByRole("button", { name: "Release mission csv-export" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "draft-release",
					arguments: { draft: "csv-export", revision: 7 },
				},
			}),
		);
	});

	it("keeps an incomplete draft out of execution", async () => {
		const state = plannedState();
		state.drafts[0].criteria = "";
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Planning" }));
		expect(screen.getByRole("button", { name: "Release mission csv-export" })).toBeDisabled();
		expect(mocks.post).not.toHaveBeenCalled();
	});

	it("preserves draft edits when a newer planner revision conflicts", async () => {
		serve(plannedState());
		mocks.post.mockResolvedValue({
			error: { message: "Draft changed. Read its latest revision" },
		});
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Planning" }));
		fireEvent.click(screen.getByRole("button", { name: "Edit draft csv-export" }));
		fireEvent.change(screen.getByLabelText("Execution plan and constraints"), {
			target: { value: "My proposed approach" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save draft" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Draft changed");
		expect(screen.getByLabelText("Execution plan and constraints")).toHaveValue("My proposed approach");
	});

	it("shows mission ownership and transfers a mission with its workers", async () => {
		const state = plannedState();
		state.agents.push({ ...state.agents[1], id: "chief-two" });
		state.agents.push({
			...state.agents[1],
			id: "worker-one",
			tier: "worker",
			role: "worker",
			parent: "chief-one",
			revision: 4,
		});
		state.missions.push({
			id: "export",
			chief: "chief-one",
			supervisor: "chief-one",
			orchestrator: "orch",
			objective: "Ship export",
			criteria: "Verified export",
			plan: "Approved plan",
			status: "active",
			revision: 3,
			result: null,
			evidence: null,
			review: null,
		});
		state.tasks.push({
			id: "csv-task",
			mission: "export",
			worker: "worker-one",
			objective: "Build export",
			status: "assigned",
			revision: 1,
			result: null,
			evidence: null,
			review: null,
		});
		serve(state);
		mount();
		const chief = await screen.findByRole("article", {
			name: "Chief chief-one",
		});
		expect(within(chief).getByText("export / active")).toBeInTheDocument();
		expect(within(chief).getByText("worker-one / worker")).toBeInTheDocument();
		fireEvent.click(within(chief).getByRole("button", { name: "Transfer mission export" }));
		fireEvent.change(screen.getByLabelText("New supervisor"), {
			target: { value: "chief-two" },
		});
		fireEvent.click(screen.getByRole("checkbox"));
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "mission-reassign",
					arguments: {
						mission: "export",
						supervisor: "chief-two",
						revision: 3,
						move_workers: true,
					},
				},
			}),
		);
	});

	it("moves a worker using its org chart revision", async () => {
		const state = structuredClone(initial);
		state.agents.push(
			{ ...state.agents[1], id: "chief-two" },
			{
				...state.agents[1],
				id: "worker-one",
				tier: "worker",
				role: "worker",
				parent: "chief-one",
				revision: 4,
			},
		);
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Move worker worker-one" }));
		fireEvent.change(screen.getByLabelText("New supervisor"), {
			target: { value: "chief-two" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-reparent",
					arguments: {
						agent: "worker-one",
						parent: "chief-two",
						revision: 4,
					},
				},
			}),
		);
	});
	it("spawns a worker with its role and supervisor through the daemon", async () => {
		mount();
		await screen.findByRole("button", { name: "Spawn agent" });
		fireEvent.click(screen.getByRole("button", { name: "Spawn agent" }));
		fireEvent.change(screen.getByLabelText("Agent ID"), {
			target: { value: "worker-one" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: {
						agent: "worker-one",
						role: "worker",
						parent: "chief-one",
						mode: "tui",
						provider: "codex",
					},
				},
			}),
		);
	});

	it("shows server validation errors while preserving form input", async () => {
		mocks.post.mockResolvedValue({
			error: { message: "Chief belongs to another orchestrator" },
		});
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Spawn agent" }));
		fireEvent.change(screen.getByLabelText("Agent ID"), {
			target: { value: "worker-one" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Chief belongs to another orchestrator");
		expect(screen.getByLabelText("Agent ID")).toHaveValue("worker-one");
	});

	it("distinguishes delivered mail from a handled receipt", async () => {
		const state = structuredClone(initial);
		state.messages.push({
			id: "msg-one",
			sender: "chief-one",
			recipient: "worker",
			subject: "Assignment",
			body: "Read the full brief",
			created_at: new Date().toISOString(),
			delivered_at: new Date().toISOString(),
			acknowledged_at: null,
		});
		mocks.get.mockImplementation(async (path: string) =>
			path === "/api/v1/projects"
				? { data: { projects: [{ id: "project", name: "My project" }] } }
				: { data: { result: state } },
		);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Mailbox" }));
		expect(screen.getByText("Delivered · pending action")).toBeInTheDocument();
		expect(screen.getByText("Read the full brief")).toBeInTheDocument();
	});

	it("requires explicit team enablement for an unconfigured project", async () => {
		mocks.get.mockImplementation(async (path: string) =>
			path === "/api/v1/projects"
				? { data: { projects: [{ id: "project", name: "My project" }] } }
				: { data: { result: { ...initial, enabled: false } } },
		);
		mount();
		expect(await screen.findByRole("button", { name: "Enable team" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Spawn agent" })).not.toBeInTheDocument();
		expect(mocks.post).not.toHaveBeenCalled();
	});

	it("spawns a worker reporting directly to the orchestrator", async () => {
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Spawn agent" }));
		expect(within(screen.getByLabelText("Supervisor")).getByRole("option", { name: "orch" })).toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("Supervisor"), { target: { value: "orch" } });
		fireEvent.change(screen.getByLabelText("Agent ID"), { target: { value: "direct-worker" } });
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: { agent: "direct-worker", role: "worker", parent: "orch", mode: "tui", provider: "codex" },
				},
			}),
		);
	});

	it("staffs a directly supervised mission with only the orchestrator's workers", async () => {
		const state = structuredClone(initial);
		state.agents.push(
			{ ...state.agents[1], id: "worker-b", tier: "worker", role: "worker", parent: "chief-one" },
			{ ...state.agents[1], id: "worker-d", tier: "worker", role: "worker", parent: "orch" },
		);
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Define mission" }));
		fireEvent.change(screen.getByLabelText("Responsible supervisor"), { target: { value: "orch" } });
		expect(screen.queryByRole("checkbox", { name: /worker-b/ })).not.toBeInTheDocument();
		fireEvent.click(screen.getByRole("checkbox", { name: /worker-d/ }));
		fireEvent.change(screen.getByLabelText("Mission ID"), { target: { value: "direct-work" } });
		fireEvent.change(screen.getByLabelText("Objective"), { target: { value: "Ship directly" } });
		fireEvent.change(screen.getByLabelText("Acceptance criteria"), { target: { value: "Verified" } });
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
			params: { path: { projectId: "project" } }, body: { operation: "mission-create", arguments: {
				mission: "direct-work", supervisor: "orch", objective: "Ship directly", criteria: "Verified", workers: ["worker-d"],
			} },
		}));
	});

	it("shows directly supervised missions and workers on the orchestrator card", async () => {
		const state = structuredClone(initial);
		state.agents.push({ ...state.agents[1], id: "worker-d", tier: "worker", role: "worker", parent: "orch" });
		state.missions.push({ id: "direct", chief: "orch", supervisor: "orch", workers: ["worker-d"], orchestrator: "orch", objective: "Ship directly", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		serve(state);
		mount();
		const card = await screen.findByRole("article", { name: "orchestrator orch" });
		expect(within(card).getByText("direct / active")).toBeInTheDocument();
		expect(within(card).getByText("worker-d / worker")).toBeInTheDocument();
		fireEvent.click(within(card).getByRole("button", { name: "Transfer mission direct" }));
		fireEvent.change(screen.getByLabelText("New supervisor"), { target: { value: "chief-one" } });
		fireEvent.click(screen.getByRole("checkbox"));
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "mission-reassign",
					arguments: { mission: "direct", supervisor: "chief-one", revision: 1, move_workers: true },
				},
			}),
		);
	});

	it("moves a direct worker to a chief from the orchestrator card", async () => {
		const state = structuredClone(initial);
		state.agents.push({ ...state.agents[1], id: "worker-d", tier: "worker", role: "worker", parent: "orch", revision: 2 });
		serve(state);
		mount();
		const card = await screen.findByRole("article", { name: "orchestrator orch" });
		fireEvent.click(within(card).getByRole("button", { name: "Move worker worker-d" }));
		fireEvent.change(screen.getByLabelText("New supervisor"), { target: { value: "chief-one" } });
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-reparent",
					arguments: { agent: "worker-d", parent: "chief-one", revision: 2 },
				},
			}),
		);
	});

	it("releases a directly supervised draft and names the orchestrator as owner", async () => {
		const state = plannedState();
		state.agents.push({ ...state.agents[1], id: "worker-d", tier: "worker", role: "worker", parent: "orch" });
		state.drafts.push({
			id: "direct-plan",
			planner: "project-planner",
			chief: "orch",
			supervisor: "orch",
			workers: ["worker-d"],
			objective: "Ship directly",
			criteria: "Verified",
			plan: "Build and verify",
			status: "draft",
			revision: 1,
			mission: null,
		});
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Planning" }));
		expect(screen.getByRole("button", { name: "Release mission direct-plan" })).toBeEnabled();
		expect(screen.getByText("Planner project-planner / proposed supervisor orch")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Release mission direct-plan" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: { operation: "draft-release", arguments: { draft: "direct-plan", revision: 1 } },
			}),
		);
	});

	it("labels directly supervised missions in the missions view", async () => {
		const state = structuredClone(initial);
		state.missions.push(
			{ id: "direct", chief: "orch", supervisor: "orch", workers: [], orchestrator: "orch", objective: "Ship directly", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null },
			{ id: "led", chief: "chief-one", supervisor: "chief-one", workers: [], orchestrator: "orch", objective: "Ship led", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null },
		);
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		expect(screen.getByText("Directly supervised by orch · Revision 1")).toBeInTheDocument();
		expect(screen.getByText("Supervisor chief-one · Final signoff orch · Revision 1")).toBeInTheDocument();
	});
});

describe("team review actions", () => {
	function reviewState() {
		const state = structuredClone(initial);
		state.agents[0].session_id = "project-1";
		state.agents[1].session_id = "project-2";
		state.agents.push(
			{ ...state.agents[1], id: "worker-d", tier: "worker", role: "worker", parent: "orch" },
			{ ...state.agents[1], id: "worker-b", tier: "worker", role: "worker", parent: "chief-one" },
		);
		return state;
	}

	it("accepts a directly supervised task as the orchestrator", async () => {
		const state = reviewState();
		state.missions.push({ id: "direct", chief: "orch", supervisor: "orch", workers: ["worker-d"], orchestrator: "orch", objective: "Ship directly", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "dt1", mission: "direct", worker: "worker-d", objective: "Do it", status: "review", revision: 2, result: "Done", evidence: "Tests", review: null });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		expect(screen.queryByRole("button", { name: "Accept as chief-one" })).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("Review feedback for dt1"), { target: { value: "Looks good" } });
		fireEvent.click(screen.getByRole("button", { name: "Accept as orch" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "review",
					arguments: { kind: "task", record: "dt1", revision: 2, approve: true, feedback: "Looks good" },
					sessionId: "project-1",
				},
			}),
		);
	});

	it("returns a chief-led task as its chief", async () => {
		const state = reviewState();
		state.missions.push({ id: "led", chief: "chief-one", supervisor: "chief-one", workers: ["worker-b"], orchestrator: "orch", objective: "Ship led", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "ct1", mission: "led", worker: "worker-b", objective: "Do it", status: "review", revision: 3, result: "Done", evidence: "Tests", review: null });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.change(screen.getByLabelText("Review feedback for ct1"), { target: { value: "Fix the edge case" } });
		fireEvent.click(screen.getByRole("button", { name: "Return as chief-one" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "review",
					arguments: { kind: "task", record: "ct1", revision: 3, approve: false, feedback: "Fix the edge case" },
					sessionId: "project-2",
				},
			}),
		);
	});

	it("submits direct mission results as the orchestrator", async () => {
		const state = reviewState();
		state.missions.push({ id: "direct", chief: "orch", supervisor: "orch", workers: ["worker-d"], orchestrator: "orch", objective: "Ship directly", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "dt1", mission: "direct", worker: "worker-d", objective: "Do it", status: "accepted", revision: 5, result: "Done", evidence: "Tests", review: "Verified" });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.change(screen.getByLabelText("Mission result for direct"), { target: { value: "Shipped" } });
		fireEvent.change(screen.getByLabelText("Verification evidence for direct"), { target: { value: "Checks green" } });
		fireEvent.click(screen.getByRole("button", { name: "Submit results as orch" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "submit",
					arguments: { kind: "mission", record: "direct", revision: 1, result: "Shipped", evidence: "Checks green" },
					sessionId: "project-1",
				},
			}),
		);
	});

	it("submits chief-led mission results as its chief", async () => {
		const state = reviewState();
		state.missions.push({ id: "led", chief: "chief-one", supervisor: "chief-one", workers: ["worker-b"], orchestrator: "orch", objective: "Ship led", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "ct1", mission: "led", worker: "worker-b", objective: "Do it", status: "accepted", revision: 3, result: "Done", evidence: "Tests", review: "Verified" });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.change(screen.getByLabelText("Mission result for led"), { target: { value: "Shipped" } });
		fireEvent.change(screen.getByLabelText("Verification evidence for led"), { target: { value: "Checks green" } });
		fireEvent.click(screen.getByRole("button", { name: "Submit results as chief-one" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "submit",
					arguments: { kind: "mission", record: "led", revision: 1, result: "Shipped", evidence: "Checks green" },
					sessionId: "project-2",
				},
			}),
		);
	});

	it("signs off a submitted mission as the orchestrator in both modes", async () => {
		for (const supervisor of ["orch", "chief-one"]) {
			mocks.post.mockClear();
			const state = reviewState();
			const id = supervisor === "orch" ? "direct" : "led";
			state.missions.push({ id, chief: supervisor, supervisor, workers: [], orchestrator: "orch", objective: "Ship", criteria: "Verified", status: "review", revision: 2, result: "Shipped", evidence: "Checks", review: null });
			serve(state);
			mount();
			fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
			fireEvent.change(screen.getByLabelText(`Review feedback for ${id}`), { target: { value: "Final signoff" } });
			fireEvent.click(screen.getByRole("button", { name: "Approve as orch" }));
			await waitFor(() =>
				expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
					params: { path: { projectId: "project" } },
					body: {
						operation: "review",
						arguments: { kind: "mission", record: id, revision: 2, approve: true, feedback: "Final signoff" },
						sessionId: "project-1",
					},
				}),
			);
			cleanup();
		}
	});

	it("hides review actions outside their valid state", async () => {
		const state = reviewState();
		state.missions.push({ id: "led", chief: "chief-one", supervisor: "chief-one", workers: ["worker-b"], orchestrator: "orch", objective: "Ship led", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "ct1", mission: "led", worker: "worker-b", objective: "Do it", status: "assigned", revision: 1, result: null, evidence: null, review: null });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		expect(screen.queryByLabelText("Review feedback for ct1")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Accept as chief-one" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Return as chief-one" })).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Submit results as chief-one" })).not.toBeInTheDocument();
	});

	it("names the detached reviewer instead of offering review actions", async () => {
		const state = reviewState();
		state.agents[0].session_id = null;
		state.missions.push({ id: "direct", chief: "orch", supervisor: "orch", workers: ["worker-d"], orchestrator: "orch", objective: "Ship directly", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "dt1", mission: "direct", worker: "worker-d", objective: "Do it", status: "review", revision: 2, result: "Done", evidence: "Tests", review: null });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		expect(await screen.findByText("Reviewer orch has no attached session.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Accept as orch" })).not.toBeInTheDocument();
	});

	it("surfaces daemon revision errors and keeps the entered feedback", async () => {
		const state = reviewState();
		state.missions.push({ id: "led", chief: "chief-one", supervisor: "chief-one", workers: ["worker-b"], orchestrator: "orch", objective: "Ship led", criteria: "Verified", status: "active", revision: 1, result: null, evidence: null, review: null });
		state.tasks.push({ id: "ct1", mission: "led", worker: "worker-b", objective: "Do it", status: "review", revision: 2, result: "Done", evidence: "Tests", review: null });
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("tab", { name: "Missions" }));
		fireEvent.change(screen.getByLabelText("Review feedback for ct1"), { target: { value: "Verified" } });
		mocks.post.mockResolvedValue({ error: { message: "State changed. Read the latest submission before reviewing" } });
		fireEvent.click(screen.getByRole("button", { name: "Accept as chief-one" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("State changed. Read the latest submission before reviewing");
		expect(screen.getByLabelText("Review feedback for ct1")).toHaveValue("Verified");
		expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
			params: { path: { projectId: "project" } },
			body: {
				operation: "review",
				arguments: { kind: "task", record: "ct1", revision: 2, approve: true, feedback: "Verified" },
				sessionId: "project-2",
			},
		});
	});
});
