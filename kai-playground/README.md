# Kai Playground

An interactive web-based playground for learning and experimenting with Kai, the intent-preserving version control system.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Browser (Frontend)                    │
│  ┌─────────────────┐  ┌─────────────────────────────┐   │
│  │  Terminal UI    │  │   Visual Graph Explorer     │   │
│  │  (xterm.js)     │  │   (D3.js / Cytoscape)       │   │
│  └────────┬────────┘  └──────────────┬──────────────┘   │
└───────────┼──────────────────────────┼──────────────────┘
            │ WebSocket                │ REST API
            ▼                          ▼
┌─────────────────────────────────────────────────────────┐
│                   Backend (Go)                          │
│  ┌─────────────────────────────────────────────────┐    │
│  │              Session Manager                     │    │
│  │   - Creates isolated temp directories per user  │    │
│  │   - Manages kai binary execution                │    │
│  │   - Cleans up expired sessions                  │    │
│  └─────────────────────────────────────────────────┘    │
│  ┌─────────────────────────────────────────────────┐    │
│  │              Example Projects                    │    │
│  │   - Pre-loaded codebases for demos              │    │
│  │   - Tutorial scenarios                          │    │
│  └─────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────┘
```

## Features

- **Interactive Terminal**: Run kai commands in a sandboxed environment
- **Visual Graph Explorer**: See snapshots, changesets, and relationships
- **Guided Tutorials**: Step-by-step lessons comparing kai vs git
- **Pre-loaded Examples**: Sample projects to experiment with

## Running Locally

### Backend
```bash
cd backend
go run .
```

### Frontend
```bash
cd frontend
npm install
npm run dev
```

## Tutorials

1. **Basic Workflow**: init, snapshot, status
2. **Workspaces vs Branches**: How kai handles parallel work
3. **Semantic Diffs**: Symbol-level change tracking
4. **Intent & Changesets**: Capturing the "why"
5. **Test Selection**: Finding affected tests
