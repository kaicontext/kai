import { Terminal } from 'xterm';
import { FitAddon } from 'xterm-addon-fit';
import { WebLinksAddon } from 'xterm-addon-web-links';
import 'xterm/css/xterm.css';
import cytoscape from 'cytoscape';

// State
let sessionId = null;
let ws = null;
let term = null;
let cy = null;
let currentTutorial = null;
let currentStep = 0;
let commandBuffer = '';

// API base URL
const API_BASE = window.location.origin;

// Initialize terminal
function initTerminal() {
    term = new Terminal({
        theme: {
            background: '#171717',
            foreground: '#ececec',
            cursor: '#ececec',
            cursorAccent: '#171717',
            selection: 'rgba(255, 255, 255, 0.2)',
            black: '#212121',
            red: '#ff6b6b',
            green: '#ececec',
            yellow: '#c0c0c0',
            blue: '#9a9a9a',
            magenta: '#b0b0b0',
            cyan: '#a0a0a0',
            white: '#ececec',
        },
        fontFamily: '"Monaco", "Menlo", "Ubuntu Mono", monospace',
        fontSize: 14,
        cursorBlink: true,
        cursorStyle: 'bar',
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(new WebLinksAddon());

    term.open(document.getElementById('terminal'));
    fitAddon.fit();

    // Handle resize
    window.addEventListener('resize', () => fitAddon.fit());

    // Handle input
    term.onData(data => {
        if (!ws || ws.readyState !== WebSocket.OPEN) {
            handleLocalInput(data);
        } else {
            handleWebSocketInput(data);
        }
    });

    return term;
}

// Handle input when using REST API (fallback)
function handleLocalInput(data) {
    const code = data.charCodeAt(0);

    if (code === 13) { // Enter
        term.writeln('');
        if (commandBuffer.trim()) {
            executeCommand(commandBuffer.trim());
        } else {
            term.write('$ ');
        }
        commandBuffer = '';
    } else if (code === 127) { // Backspace
        if (commandBuffer.length > 0) {
            commandBuffer = commandBuffer.slice(0, -1);
            term.write('\b \b');
        }
    } else if (code >= 32) { // Printable characters
        commandBuffer += data;
        term.write(data);
    }
}

// Handle WebSocket input
function handleWebSocketInput(data) {
    const code = data.charCodeAt(0);

    if (code === 13) { // Enter
        term.writeln('');
        if (commandBuffer.trim()) {
            ws.send(commandBuffer.trim());
        } else {
            term.write('$ ');
        }
        commandBuffer = '';
    } else if (code === 127) { // Backspace
        if (commandBuffer.length > 0) {
            commandBuffer = commandBuffer.slice(0, -1);
            term.write('\b \b');
        }
    } else if (code >= 32) {
        commandBuffer += data;
        term.write(data);
    }
}

// Execute command via REST API
async function executeCommand(command) {
    if (!sessionId) {
        term.writeln('\x1b[31mNo session. Creating one...\x1b[0m');
        await createSession();
        term.write('$ ');
        return;
    }

    try {
        const response = await fetch(`${API_BASE}/api/command?session=${sessionId}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ command }),
        });

        const result = await response.json();
        if (result.output) {
            term.writeln(result.output.replace(/\n$/, ''));
        }
        if (result.error && !result.output.includes('Error')) {
            term.writeln(`\x1b[31mError executing command\x1b[0m`);
        }
    } catch (err) {
        term.writeln(`\x1b[31mError: ${err.message}\x1b[0m`);
    }

    term.write('$ ');

    // Refresh graph after commands that might change state
    if (command.includes('snapshot') || command.includes('init') || command.includes('analyze')) {
        setTimeout(refreshGraph, 500);
    }
}

// Create a new session
async function createSession() {
    try {
        const response = await fetch(`${API_BASE}/api/session`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ example: 'basic' }),
        });

        const data = await response.json();
        sessionId = data.sessionId;

        // Try to connect WebSocket - it will send the welcome message
        connectWebSocket();

        return sessionId;
    } catch (err) {
        term.writeln(`\x1b[31mFailed to create session: ${err.message}\x1b[0m`);
        term.writeln('Make sure the backend server is running.');
        return null;
    }
}

// Connect WebSocket for real-time terminal
function connectWebSocket() {
    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${wsProtocol}//${window.location.host}/api/terminal?session=${sessionId}`;

    try {
        ws = new WebSocket(wsUrl);

        ws.onopen = () => {
            console.log('WebSocket connected');
        };

        ws.onmessage = (event) => {
            term.write(event.data);
        };

        ws.onclose = () => {
            console.log('WebSocket closed');
            ws = null;
        };

        ws.onerror = (err) => {
            console.log('WebSocket error, falling back to REST', err);
            ws = null;
        };
    } catch (err) {
        console.log('WebSocket not available, using REST API');
    }
}

// Initialize graph visualization
function initGraph() {
    cy = cytoscape({
        container: document.getElementById('graph'),
        style: [
            {
                selector: 'node',
                style: {
                    'label': 'data(label)',
                    'text-valign': 'bottom',
                    'text-halign': 'center',
                    'font-size': '10px',
                    'color': '#656d76',
                    'text-margin-y': 5,
                },
            },
            {
                selector: 'node[type="snapshot"]',
                style: {
                    'background-color': '#0969da',
                    'width': 30,
                    'height': 30,
                },
            },
            {
                selector: 'node[type="file"]',
                style: {
                    'background-color': '#1a7f37',
                    'width': 20,
                    'height': 20,
                },
            },
            {
                selector: 'node[type="symbol"]',
                style: {
                    'background-color': '#9a6700',
                    'width': 15,
                    'height': 15,
                },
            },
            {
                selector: 'edge',
                style: {
                    'width': 1,
                    'line-color': '#d0d7de',
                    'target-arrow-color': '#d0d7de',
                    'target-arrow-shape': 'triangle',
                    'curve-style': 'bezier',
                },
            },
            {
                selector: 'edge[type="TESTS"]',
                style: {
                    'line-color': '#1a7f37',
                    'target-arrow-color': '#1a7f37',
                },
            },
            {
                selector: 'edge[type="IMPORTS"]',
                style: {
                    'line-color': '#0969da',
                    'target-arrow-color': '#0969da',
                },
            },
        ],
        layout: {
            name: 'cose',
            animate: false,
        },
    });

    // Show placeholder
    showGraphPlaceholder();
}

function showGraphPlaceholder() {
    cy.add([
        { data: { id: 'placeholder', label: 'Run "kai init" to start', type: 'snapshot' } },
    ]);
    cy.layout({ name: 'preset', positions: { placeholder: { x: 150, y: 100 } } }).run();
}

// Refresh graph data
async function refreshGraph() {
    if (!sessionId) return;

    try {
        // For now, just show a simple representation
        // In a full implementation, we'd parse the kai dump output
        cy.elements().remove();

        // Add some placeholder nodes to show the concept
        cy.add([
            { data: { id: 'snap1', label: 'Snapshot', type: 'snapshot' } },
            { data: { id: 'file1', label: 'app.js', type: 'file' } },
            { data: { id: 'file2', label: 'math.js', type: 'file' } },
            { data: { id: 'file3', label: 'app.test.js', type: 'file' } },
            { data: { id: 'e1', source: 'snap1', target: 'file1', type: 'HAS_FILE' } },
            { data: { id: 'e2', source: 'snap1', target: 'file2', type: 'HAS_FILE' } },
            { data: { id: 'e3', source: 'snap1', target: 'file3', type: 'HAS_FILE' } },
            { data: { id: 'e4', source: 'file1', target: 'file2', type: 'IMPORTS' } },
            { data: { id: 'e5', source: 'file3', target: 'file1', type: 'TESTS' } },
        ]);

        cy.layout({ name: 'cose', animate: true, animationDuration: 500 }).run();
    } catch (err) {
        console.error('Failed to refresh graph:', err);
    }
}

// Tutorial handling
async function loadTutorial(tutorialId) {
    if (tutorialId === 'intro') {
        document.getElementById('tutorial-content').style.display = 'block';
        document.getElementById('tutorial-steps').style.display = 'none';
        currentTutorial = null;
        return;
    }

    try {
        const response = await fetch(`${API_BASE}/api/tutorial?id=${tutorialId}`);
        currentTutorial = await response.json();
        currentStep = 0;
        showTutorialStep();
    } catch (err) {
        console.error('Failed to load tutorial:', err);
        // Show offline tutorial data
        currentTutorial = getOfflineTutorial(tutorialId);
        if (currentTutorial) {
            currentStep = 0;
            showTutorialStep();
        }
    }
}

function getOfflineTutorial(id) {
    const tutorials = {
        basics: {
            title: 'Kai Basics',
            steps: [
                {
                    title: 'Initialize a repository',
                    description: 'Just like git, kai needs to be initialized. This creates a .kai folder.',
                    commands: ['kai init'],
                    gitCompare: 'git init',
                },
                {
                    title: 'Create a snapshot',
                    description: 'Snapshots capture your entire project state. Kai also analyzes your code to understand symbols.',
                    commands: ['kai snapshot --dir .'],
                    gitCompare: 'git add . && git commit -m "message"',
                },
                {
                    title: 'Check status',
                    description: 'See what changed since your last snapshot.',
                    commands: ['kai status'],
                    gitCompare: 'git status',
                },
                {
                    title: 'View history',
                    description: 'See your snapshot history.',
                    commands: ['kai log'],
                    gitCompare: 'git log',
                },
            ],
        },
        'affected-tests': {
            title: 'Finding Affected Tests',
            steps: [
                {
                    title: 'Setup',
                    description: 'Initialize kai and create a baseline.',
                    commands: ['kai init', 'kai snapshot --dir .'],
                },
                {
                    title: 'Build the call graph',
                    description: 'Analyze imports and function calls.',
                    commands: ['kai analyze calls @snap:last'],
                },
                {
                    title: 'Make a change',
                    description: 'Modify a file and create a new snapshot.',
                    commands: ['echo "// changed" >> src/utils/math.js', 'kai snapshot --dir .', 'kai analyze calls @snap:last'],
                },
                {
                    title: 'Find affected tests',
                    description: 'See which tests need to run based on your changes.',
                    commands: ['kai test affected @snap:prev @snap:last'],
                    gitCompare: 'Git cannot do this - you need external tools or run all tests.',
                },
            ],
        },
    };
    return tutorials[id];
}

function showTutorialStep() {
    if (!currentTutorial) return;

    document.getElementById('tutorial-content').style.display = 'none';
    document.getElementById('tutorial-steps').style.display = 'flex';

    const step = currentTutorial.steps[currentStep];
    const stepContent = document.getElementById('step-content');

    // Convert newlines to <br> for description
    const description = step.description.replace(/\n/g, '<br>');
    let html = `<h3>${step.title}</h3><p>${description}</p>`;

    if (step.commands && step.commands.length > 0) {
        html += `<div class="step-commands"><h4>Commands (click to copy to terminal):</h4>`;
        step.commands.forEach((cmd, idx) => {
            // Escape single quotes for onclick
            const escapedCmd = cmd.replace(/'/g, "\\'").replace(/"/g, '&quot;');
            html += `<code data-cmd-idx="${idx}">${escapeHtml(cmd)}</code>`;
        });
        html += `</div>`;
    }

    if (step.gitCompare) {
        html += `<div class="git-compare"><h4>Git equivalent:</h4><p>${step.gitCompare}</p></div>`;
    }

    stepContent.innerHTML = html;

    // Add click handlers to commands
    if (step.commands && step.commands.length > 0) {
        stepContent.querySelectorAll('[data-cmd-idx]').forEach((el) => {
            const idx = parseInt(el.dataset.cmdIdx);
            el.addEventListener('click', () => copyCommand(step.commands[idx]));
        });
    }

    // Update navigation (both top and bottom)
    document.querySelectorAll('.step-indicator').forEach(el => {
        el.textContent = `Step ${currentStep + 1} of ${currentTutorial.steps.length}`;
    });
    document.querySelectorAll('.prev-step').forEach(el => {
        el.disabled = currentStep === 0;
    });
    document.querySelectorAll('.next-step').forEach(el => {
        el.disabled = currentStep === currentTutorial.steps.length - 1;
    });
}

// Helper to escape HTML
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// Copy command to terminal
window.copyCommand = function(command) {
    if (term) {
        // Clear current buffer and write command
        while (commandBuffer.length > 0) {
            term.write('\b \b');
            commandBuffer = commandBuffer.slice(0, -1);
        }
        commandBuffer = command;
        term.write(command);
    }
};

// Event listeners
document.addEventListener('DOMContentLoaded', async () => {
    // Initialize terminal
    initTerminal();

    // Initialize graph
    initGraph();

    // Create session (WebSocket will send the welcome message with prompt)
    await createSession();

    // Tutorial navigation
    document.querySelectorAll('.tutorial-btn').forEach(btn => {
        btn.addEventListener('click', () => {
            document.querySelectorAll('.tutorial-btn').forEach(b => b.classList.remove('active'));
            btn.classList.add('active');
            loadTutorial(btn.dataset.tutorial);
        });
    });

    // Step navigation (both top and bottom)
    document.querySelectorAll('.prev-step').forEach(btn => {
        btn.addEventListener('click', () => {
            if (currentStep > 0) {
                currentStep--;
                showTutorialStep();
            }
        });
    });

    document.querySelectorAll('.next-step').forEach(btn => {
        btn.addEventListener('click', () => {
            if (currentTutorial && currentStep < currentTutorial.steps.length - 1) {
                currentStep++;
                showTutorialStep();
            }
        });
    });

    // Clear terminal
    document.getElementById('clear-terminal').addEventListener('click', () => {
        term.clear();
        term.write('$ ');
        commandBuffer = '';
    });

    // Refresh graph
    document.getElementById('refresh-graph').addEventListener('click', refreshGraph);
});
