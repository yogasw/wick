export const START_PROMPT = `Check if Go is installed by running: go version
If Go is not installed, help me install it first for my OS, then come back to this.

Once Go is ready:
1. Install wick: go install github.com/yogasw/wick@v0.1.12
2. Ask me: what do you want to name your project?
3. Scaffold it: wick init <name>
4. Start the dev server and show me what was created.`
