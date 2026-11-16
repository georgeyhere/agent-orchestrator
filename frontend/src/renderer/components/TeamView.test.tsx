import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { TeamView, type TeamState } from "./TeamView";

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.get, POST: mocks.post },
	apiErrorMessage: (error: { message: string }) => error.message,
}));
vi.mock("@tanstack/react-router", () => ({ Link: ({ children }: { children: ReactNode }) => <a>{children}</a> }));
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
		},
	],
	missions: [],
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
