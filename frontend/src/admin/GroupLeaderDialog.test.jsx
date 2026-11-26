import { describe, it, expect, vi, beforeEach } from "vitest";
import userEvent from "@testing-library/user-event";
import { render, screen } from "@testing-library/react";

const { usersOfGroup, setGroupLeader } = vi.hoisted(() => ({ usersOfGroup: vi.fn(), setGroupLeader: vi.fn() }));
vi.mock("../api/endpoints", () => ({ api: { usersOfGroup, setGroupLeader } }));
import GroupLeaderDialog from "./GroupLeaderDialog.jsx";

// jsdom does not implement <dialog>.showModal
beforeEach(() => {
  HTMLDialogElement.prototype.showModal ??= function showModal() { this.open = true; };
  HTMLDialogElement.prototype.close ??= function close() { this.open = false; this.dispatchEvent(new Event("close")); };
  usersOfGroup.mockReset().mockResolvedValue([
    { user_id: "u-alice", username: "alice", status: "Active" },
    { user_id: "u-carol", username: "carol", status: "Active" },
  ]);
  setGroupLeader.mockReset().mockResolvedValue({ status: "success" });
});

const group = { id: "g-1", name: "analysts", leader_name: "" };

describe("GroupLeaderDialog", () => {
  it("offers the group's members and saves the chosen leader", async () => {
    const onSaved = vi.fn(), onClose = vi.fn();
    const user = userEvent.setup();
    render(<GroupLeaderDialog group={group} onClose={onClose} onSaved={onSaved} />);
    await user.selectOptions(await screen.findByLabelText("Leader"), "carol");
    await user.click(screen.getByRole("button", { name: "Set leader" }));
    expect(setGroupLeader).toHaveBeenCalledWith("u-carol", "g-1");
    expect(onSaved).toHaveBeenCalledWith("carol is now the leader of analysts.");
  });

  it("explains a group without a leader and blocks saving until one is chosen", async () => {
    render(<GroupLeaderDialog group={group} onClose={() => {}} onSaved={() => {}} />);
    expect(await screen.findByText(/has no leader yet/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set leader" })).toBeDisabled();
  });

  it("preselects the current leader and blocks saving without a change", async () => {
    render(<GroupLeaderDialog group={{ ...group, leader_name: "alice" }} onClose={() => {}} onSaved={() => {}} />);
    expect(await screen.findByLabelText("Leader")).toHaveValue("u-alice");
    expect(screen.getByRole("button", { name: "Change leader" })).toBeDisabled();
  });

  it("warns when the current leader has left the group", async () => {
    render(<GroupLeaderDialog group={{ ...group, leader_name: "dave" }} onClose={() => {}} onSaved={() => {}} />);
    expect(await screen.findByText(/dave is no longer a member/)).toBeInTheDocument();
  });

  it("asks for members first when the group is empty", async () => {
    usersOfGroup.mockResolvedValue([]);
    render(<GroupLeaderDialog group={group} onClose={() => {}} onSaved={() => {}} />);
    expect(await screen.findByText(/no members/)).toBeInTheDocument();
  });

  it("shows the server's refusal and stays open", async () => {
    setGroupLeader.mockRejectedValue(new Error("Your access to the admin console is view only"));
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<GroupLeaderDialog group={group} onClose={onClose} onSaved={() => {}} />);
    await user.selectOptions(await screen.findByLabelText("Leader"), "alice");
    await user.click(screen.getByRole("button", { name: "Set leader" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("view only");
    expect(onClose).not.toHaveBeenCalled();
  });
});
