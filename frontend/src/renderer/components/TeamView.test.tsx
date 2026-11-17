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
		{ id: "orchestrator", tier: "orchestrator", instructions: "Sign off missions" },
		{ id: "chief", tier: "chief", instructions: "Drive workers" },
		{ id: "worker", tier: "worker", instructions: "Implement tasks" },
		{ id: "planner", tier: "planner", instructions: "Plan missions with the user" },
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
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
	it("creates a project planner in chat mode without a supervisor", async () => {
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Create planner" }));
		expect(screen.queryByLabelText("Supervisor")).not.toBeInTheDocument();
		fireEvent.change(screen.getByLabelText("Agent ID"), { target: { value: "planner-one" } });
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: { agent: "planner-one", role: "planner", parent: null, mode: "chat", provider: "codex" },
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
		fireEvent.change(screen.getByLabelText("Draft ID"), { target: { value: "new-goal" } });
		fireEvent.change(screen.getByLabelText("Mission objective"), { target: { value: "Explore export requirements" } });
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
						chief: null,
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
				body: { operation: "draft-release", arguments: { draft: "csv-export", revision: 7 } },
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
		mocks.post.mockResolvedValue({ error: { message: "Draft changed. Read its latest revision" } });
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
		const chief = await screen.findByRole("article", { name: "Chief chief-one" });
		expect(within(chief).getByText("export / active")).toBeInTheDocument();
		expect(within(chief).getByText("worker-one / worker")).toBeInTheDocument();
		fireEvent.click(within(chief).getByRole("button", { name: "Transfer mission export" }));
		fireEvent.change(screen.getByLabelText("New chief"), { target: { value: "chief-two" } });
		fireEvent.click(screen.getByRole("checkbox"));
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "mission-reassign",
					arguments: { mission: "export", chief: "chief-two", revision: 3, move_workers: true },
				},
			}),
		);
	});

	it("moves a worker using its org chart revision", async () => {
		const state = structuredClone(initial);
		state.agents.push(
			{ ...state.agents[1], id: "chief-two" },
			{ ...state.agents[1], id: "worker-one", tier: "worker", role: "worker", parent: "chief-one", revision: 4 },
		);
		serve(state);
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Move worker worker-one" }));
		fireEvent.change(screen.getByLabelText("New chief"), { target: { value: "chief-two" } });
		fireEvent.click(screen.getByRole("button", { name: "Save assignment" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: { operation: "agent-reparent", arguments: { agent: "worker-one", parent: "chief-two", revision: 4 } },
			}),
		);
	});
	it("spawns a worker with its role and supervisor through the daemon", async () => {
		mount();
		await screen.findByRole("button", { name: "Spawn agent" });
		fireEvent.click(screen.getByRole("button", { name: "Spawn agent" }));
		fireEvent.change(screen.getByLabelText("Agent ID"), { target: { value: "worker-one" } });
		fireEvent.click(screen.getByRole("button", { name: "Save" }));
		await waitFor(() =>
			expect(mocks.post).toHaveBeenCalledWith("/api/v1/projects/{projectId}/team", {
				params: { path: { projectId: "project" } },
				body: {
					operation: "agent-add",
					arguments: { agent: "worker-one", role: "worker", parent: "chief-one", mode: "tui", provider: "codex" },
				},
			}),
		);
	});

	it("shows server validation errors while preserving form input", async () => {
		mocks.post.mockResolvedValue({ error: { message: "Chief belongs to another orchestrator" } });
		mount();
		fireEvent.click(await screen.findByRole("button", { name: "Spawn agent" }));
		fireEvent.change(screen.getByLabelText("Agent ID"), { target: { value: "worker-one" } });
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
});
