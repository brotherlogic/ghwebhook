# Project Instructions: GHWebhook

These instructions are foundational mandates for development in the `ghwebhook` repository. All changes must adhere to these standards.

## 1. Development Workflow (Issue-Driven)

Every feature or bug fix MUST follow this sequence:
1.  **Issue Research & Alignment:** Before implementation, the agent and user must reach a shared understanding of requirements. This conclusion MUST be posted as a comment on the GitHub issue.
2.  **Implementation Plan:** A detailed implementation plan MUST be posted as a comment on the GitHub issue after alignment.
3.  **TDD Execution:** Implementation work MUST NOT start until the two documents above are posted.
4.  **Completion:** Every issue is closed by a Pull Request (PR) that references the issue.

## 2. Coding Standards

### Test-Driven Development (TDD)
-   Follow **Matt Pocock's TDD cycle**: 
    1.  **Red:** Write a failing test that defines the expected behavior.
    2.  **Green:** Implement the minimal code required to make the test pass.
    3.  **Refactor:** Clean up the code while ensuring tests stay green.
-   **Testing Framework:** Use the standard Go `testing` package.
-   **Mocking:** Use standard mocking techniques (interfaces and manual or generated mocks) where necessary, especially for gRPC and `pstore` dependencies.

### Commit & PR Standards
-   **Conventional Commits:** Use the `type: description` format (e.g., `feat:`, `fix:`, `docs:`, `test:`, `refactor:`).
-   **Pull Requests:** Every PR must link to its corresponding issue and briefly summarize the TDD process used.

### Architectural Mandates
-   **Registration:** Must use the gRPC Registration API with persistence via `github.com/brotherlogic/pstore`.
-   **Security:** Mandatory GitHub Webhook Secret validation (`X-Hub-Signature-256`).
-   **Routing:** Support 1-to-N mapping for repositories to services.
-   **Communication:** Internal communication between proxy and handlers must be gRPC-based using the defined proto structures.

## 3. Tooling
-   **Makefile:** Use the `Makefile` for proto generation and common tasks.
-   **Go Modules:** Ensure `go mod tidy` is run after adding dependencies.
