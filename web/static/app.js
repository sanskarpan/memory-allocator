// Memory Allocator Simulator - Frontend Application

class MemorySimulator {
    constructor() {
        this.ws = null;
        this.canvas = document.getElementById('memory-canvas');
        this.ctx = this.canvas.getContext('2d');
        this.blocks = [];
        this.totalSize = 0;
        this.selectedBlock = null;
        this.state = null;
        this.allocatorType = null;
        this.connected = false;
        this.initialized = false;
        this.reconnectAttempts = 0;
        this.maxReconnectAttempts = 10;
        this.reconnectTimer = null;
        this.intentionalClose = false;
        this.blockPositions = new Map();

        this.init();
    }

    init() {
        this.connectWebSocket();
        this.setupEventListeners();
        this.updateCanvasSize();
        this.visualizeMemory();
    }

    connectWebSocket() {
        if (this.reconnectAttempts >= this.maxReconnectAttempts) {
            this.updateStatus('Connection failed (max retries)', false);
            return;
        }
        // Cancel any pending reconnect to avoid duplicate timers.
        if (this.reconnectTimer) {
            clearTimeout(this.reconnectTimer);
            this.reconnectTimer = null;
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsURL = `${protocol}//${window.location.host}/ws`;

        try {
            this.ws = new WebSocket(wsURL);
        } catch (err) {
            this.updateStatus('Connection error', false);
            this.scheduleReconnect();
            return;
        }

        this.ws.onopen = () => {
            this.connected = true;
            this.reconnectAttempts = 0;
            this.updateStatus('Connected', true);
            this.addLogEntry('Connected to server', 'success');
            // If the server still has a simulator (e.g. we reconnected after
            // a temporary network blip), the next state update from the
            // server will mark us as initialized again.
            this.refreshButtonStates();
        };

        this.ws.onmessage = (event) => {
            let data;
            try {
                data = JSON.parse(event.data);
            } catch (err) {
                this.addLogEntry('Invalid message from server', 'error');
                return;
            }
            this.handleMessage(data);
        };

        this.ws.onerror = () => {
            // onerror always fires before onclose. Don't reconnect here;
            // onclose will handle the reconnect to avoid double-scheduling.
            this.updateStatus('Connection error', false);
            this.addLogEntry('WebSocket error', 'error');
        };

        this.ws.onclose = () => {
            this.connected = false;
            this.initialized = false;
            this.selectedBlock = null;
            this.updateStatus('Disconnected', false);
            this.addLogEntry('Disconnected from server', 'error');
            this.refreshButtonStates();
            if (!this.intentionalClose) {
                this.scheduleReconnect();
            }
        };
    }

    scheduleReconnect() {
        if (this.reconnectTimer) {
            return;
        }
        if (this.reconnectAttempts >= this.maxReconnectAttempts) {
            this.updateStatus('Connection failed (max retries)', false);
            return;
        }
        this.reconnectAttempts++;
        const delay = Math.min(30000, 2000 * this.reconnectAttempts);
        this.reconnectTimer = setTimeout(() => {
            this.reconnectTimer = null;
            this.connectWebSocket();
        }, delay);
    }

    disconnect() {
        this.intentionalClose = true;
        if (this.ws) {
            this.ws.close();
        }
    }

    setupEventListeners() {
        document.getElementById('allocator-type').addEventListener('change', (e) => {
            const poolOptions = document.querySelector('.pool-options');
            poolOptions.style.display = e.target.value === 'pool' ? 'block' : 'none';
        });

        document.getElementById('init-btn').addEventListener('click', () => this.initialize());

        document.getElementById('start-btn').addEventListener('click', () => this.start());
        document.getElementById('pause-btn').addEventListener('click', () => this.pause());
        document.getElementById('resume-btn').addEventListener('click', () => this.resume());
        document.getElementById('stop-btn').addEventListener('click', () => this.stop());
        document.getElementById('reset-btn').addEventListener('click', () => this.reset());

        document.getElementById('allocate-btn').addEventListener('click', () => this.allocate());
        document.getElementById('deallocate-btn').addEventListener('click', () => this.deallocate());
        document.getElementById('coalesce-btn').addEventListener('click', () => this.coalesce());
        document.getElementById('detect-leaks-btn').addEventListener('click', () => this.detectLeaks());

        const speedSlider = document.getElementById('speed-slider');
        speedSlider.addEventListener('input', (e) => {
            document.getElementById('speed-value').textContent = `${e.target.value} ms`;
            if (this.initialized) {
                this.setSpeed(parseInt(e.target.value));
            }
        });

        this.canvas.addEventListener('click', (e) => this.handleCanvasClick(e));
        this.canvas.addEventListener('keydown', (e) => this.handleCanvasKeydown(e));
        window.addEventListener('resize', () => this.updateCanvasSize());
    }

    refreshButtonStates() {
        const connected = this.connected;
        const initialized = this.initialized;
        const running = this.state === 1;
        const paused = this.state === 2;

        document.getElementById('init-btn').disabled = !connected;
        document.getElementById('start-btn').disabled = !initialized || running || paused;
        document.getElementById('pause-btn').disabled = !initialized || !running;
        document.getElementById('resume-btn').disabled = !initialized || !paused;
        document.getElementById('stop-btn').disabled = !initialized || (!running && !paused);
        document.getElementById('reset-btn').disabled = !initialized;
        document.getElementById('allocate-btn').disabled = !initialized;
        document.getElementById('deallocate-btn').disabled = !this.selectedBlock;
        document.getElementById('coalesce-btn').disabled = !initialized;
        document.getElementById('detect-leaks-btn').disabled = !initialized;
    }

    sendMessage(message) {
        if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
            this.addLogEntry('Not connected to server', 'error');
            return false;
        }
        try {
            this.ws.send(JSON.stringify(message));
            return true;
        } catch (err) {
            this.addLogEntry(`Send failed: ${err.message || err}`, 'error');
            return false;
        }
    }

    handleMessage(data) {
        if (data.type === 'success') {
            this.addLogEntry(data.message, 'success');
            return;
        }
        if (data.type === 'error') {
            this.addLogEntry(data.message, 'error');
            return;
        }
        if (data.type === 'leaksResult') {
            this.displayLeaks(data.leaks || []);
            this.addLogEntry(data.message, 'info');
            return;
        }
        // State update: a state with a non-null allocatorType means the
        // server has a simulator ready, so we're initialized.
        const prevState = this.state;
        const prevInit = this.initialized;
        this.state = data.state;
        this.blocks = data.blocks || [];
        this.totalSize = data.totalSize || 0;
        this.allocatorType = data.allocatorType;
        this.initialized = !!data.allocatorType;
        this.updateUI(data);
        if (prevState !== this.state || prevInit !== this.initialized) {
            this.refreshButtonStates();
        }
    }

    initialize() {
        const allocType = document.getElementById('allocator-type').value;
        const size = parseInt(document.getElementById('memory-size').value, 10);
        const blockSize = parseInt(document.getElementById('block-size').value, 10);

        if (!size || size < 64) {
            this.addLogEntry('Memory size must be at least 64 bytes', 'error');
            return;
        }
        if (allocType === 'pool' && (!blockSize || blockSize < 64)) {
            this.addLogEntry('Block size must be at least 64 bytes', 'error');
            return;
        }

        // Do NOT mark as initialized here — wait for the first state
        // update from the server so that the UI buttons reflect the
        // actual server state, not an optimistic guess.
        this.sendMessage({
            type: 'init',
            allocator: allocType,
            size: size,
            blockSize: blockSize,
        });
    }

    start() {
        this.sendMessage({ type: 'start' });
    }

    pause() {
        this.sendMessage({ type: 'pause' });
    }

    resume() {
        this.sendMessage({ type: 'resume' });
    }

    stop() {
        this.sendMessage({ type: 'stop' });
    }

    reset() {
        this.sendMessage({ type: 'reset' });
        this.selectedBlock = null;
        document.getElementById('selected-block-info').style.display = 'none';
        document.getElementById('events-log').innerHTML = '';
        document.getElementById('leaks-panel').style.display = 'none';
    }

    allocate() {
        const size = parseInt(document.getElementById('alloc-size').value, 10);
        const owner = document.getElementById('alloc-owner').value || 'User';
        if (!size || size < 1) {
            this.addLogEntry('Allocation size must be positive', 'error');
            return;
        }
        this.sendMessage({ type: 'allocate', size, owner });
    }

    deallocate() {
        if (!this.selectedBlock || !this.selectedBlock.isAllocated) {
            this.addLogEntry('Select an allocated block first', 'error');
            return;
        }
        this.sendMessage({ type: 'deallocate', address: this.selectedBlock.address });
        this.selectedBlock = null;
        document.getElementById('selected-block-info').style.display = 'none';
        this.refreshButtonStates();
    }

    coalesce() {
        this.sendMessage({ type: 'coalesce' });
    }

    detectLeaks() {
        this.sendMessage({ type: 'detectLeaks', threshold: 5 });
    }

    setSpeed(speed) {
        this.sendMessage({ type: 'speed', speed });
    }

    updateUI(data) {
        this.updateMetrics(data.metrics);
        this.updateAllocatorInfo(data.allocatorName, data.state);
        this.visualizeMemory();
        this.updateBlockListSR();
        if (data.leaks && data.leaks.length > 0) {
            this.displayLeaks(data.leaks);
        }
    }

    updateBlockListSR() {
        const sr = document.getElementById('block-list-sr');
        if (!sr || !this.blocks || this.blocks.length === 0) {
            if (sr) sr.textContent = '';
            return;
        }
        const sorted = [...this.blocks].sort((a, b) => Number(a.address) - Number(b.address));
        const summary = sorted.map(b => {
            const state = b.state === 1 ? 'allocated' : 'free';
            const owner = b.state === 1 && b.owner ? ` (${this.escapeHtml(b.owner)})` : '';
            return `Block ${b.id}: ${this.formatBytes(b.size)}, ${state}${owner}`;
        }).join(', ');
        sr.textContent = `${sorted.length} blocks: ${summary}`;
    }

    updateMetrics(metrics) {
        document.getElementById('metric-allocations').textContent = metrics.totalAllocations || 0;
        document.getElementById('metric-deallocations').textContent = metrics.totalDeallocations || 0;
        document.getElementById('metric-current').textContent = this.formatBytes(metrics.currentBytesUsed || 0);
        document.getElementById('metric-peak').textContent = this.formatBytes(metrics.peakBytesUsed || 0);
        document.getElementById('metric-fragmentation').textContent = `${(metrics.fragmentation || 0).toFixed(1)}%`;
        document.getElementById('metric-utilization').textContent = `${(metrics.utilization || 0).toFixed(1)}%`;
        document.getElementById('metric-failed').textContent = metrics.failedAllocations || 0;
        document.getElementById('metric-avg-time').textContent = this.formatNanoseconds(metrics.averageAllocTime || 0);
    }

    updateAllocatorInfo(name, state) {
        const stateMap = { 0: 'Idle', 1: 'Running', 2: 'Paused', 3: 'Complete' };
        document.getElementById('allocator-info').textContent = `${name || 'None'} - ${stateMap[state] || 'Unknown'}`;
    }

    visualizeMemory() {
        const ctx = this.ctx;
        const width = this.canvas.width;
        const height = this.canvas.height;

        ctx.clearRect(0, 0, width, height);
        this.blockPositions.clear();

        if (!this.blocks || this.blocks.length === 0 || this.totalSize === 0) {
            this.drawEmptyState(ctx, width, height);
            return;
        }

        // Sort blocks by address for stable layout
        const sorted = [...this.blocks].sort((a, b) => Number(a.address) - Number(b.address));

        const padding = 20;
        const availableWidth = width - 2 * padding;
        // Layout in a single horizontal bar, scaled to totalSize.
        let x = padding;
        const y = padding + 30;
        const barHeight = 60;

        // Draw a frame
        ctx.fillStyle = '#f7fafc';
        ctx.fillRect(padding, y, availableWidth, barHeight);
        ctx.strokeStyle = '#cbd5e0';
        ctx.lineWidth = 1;
        ctx.strokeRect(padding, y, availableWidth, barHeight);

        // Map each block to its render position
        for (const block of sorted) {
            const w = Math.max(2, (block.size / this.totalSize) * availableWidth);
            const isAllocated = block.state === 1;
            const fill = isAllocated ? (block.color || '#667eea') : '#e2e8f0';

            ctx.fillStyle = fill;
            ctx.fillRect(x, y, w, barHeight);

            const isSelected = this.selectedBlock && this.selectedBlock.address === block.address;
            ctx.strokeStyle = isSelected ? '#f56565' : '#cbd5e0';
            ctx.lineWidth = isSelected ? 3 : 1;
            ctx.strokeRect(x, y, w, barHeight);

            // Save the position
            this.blockPositions.set(block.address, { x, y, w, h: barHeight, block });

            // Draw the size label if there's enough space
            if (w > 30) {
                ctx.fillStyle = isAllocated ? 'white' : '#4a5568';
                ctx.font = '11px Arial';
                ctx.textAlign = 'center';
                ctx.fillText(this.formatBytes(block.size), x + w / 2, y + barHeight / 2 + 4);
            }
            x += w;
        }

        this.drawLegend(ctx, width, height);
    }

    drawEmptyState(ctx, width, height) {
        ctx.fillStyle = '#f7fafc';
        ctx.fillRect(0, 0, width, height);
        ctx.strokeStyle = '#cbd5e0';
        ctx.lineWidth = 2;
        ctx.strokeRect(10, 10, width - 20, height - 20);
        ctx.fillStyle = '#4a5568';
        ctx.font = 'bold 24px Arial';
        ctx.textAlign = 'center';
        ctx.fillText('Memory Visualization', width / 2, height / 2 - 40);
        ctx.font = '16px Arial';
        ctx.fillStyle = '#718096';
        ctx.fillText('Click "Initialize Allocator" above to get started', width / 2, height / 2);
        ctx.fillText('Select an algorithm and memory size, then click initialize', width / 2, height / 2 + 30);
    }

    drawLegend(ctx, width, height) {
        const legendY = height - 40;
        const legendX = 20;
        ctx.fillStyle = '#e2e8f0';
        ctx.fillRect(legendX, legendY, 30, 20);
        ctx.strokeStyle = '#cbd5e0';
        ctx.strokeRect(legendX, legendY, 30, 20);
        ctx.fillStyle = '#4a5568';
        ctx.font = '14px Arial';
        ctx.textAlign = 'left';
        ctx.fillText('Free', legendX + 40, legendY + 15);

        ctx.fillStyle = '#667eea';
        ctx.fillRect(legendX + 100, legendY, 30, 20);
        ctx.strokeStyle = '#cbd5e0';
        ctx.strokeRect(legendX + 100, legendY, 30, 20);
        ctx.fillStyle = '#4a5568';
        ctx.fillText('Allocated', legendX + 140, legendY + 15);

        ctx.strokeStyle = '#f56565';
        ctx.lineWidth = 3;
        ctx.strokeRect(legendX + 220, legendY, 30, 20);
        ctx.fillStyle = '#4a5568';
        ctx.fillText('Selected', legendX + 260, legendY + 15);
    }

    handleCanvasClick(event) {
        const rect = this.canvas.getBoundingClientRect();
        const x = event.clientX - rect.left;
        const y = event.clientY - rect.top;
        const scaleX = this.canvas.width / rect.width;
        const scaleY = this.canvas.height / rect.height;
        const cx = x * scaleX;
        const cy = y * scaleY;

        for (const pos of this.blockPositions.values()) {
            if (cx >= pos.x && cx <= pos.x + pos.w && cy >= pos.y && cy <= pos.y + pos.h) {
                this.selectBlock(pos.block);
                return;
            }
        }
        this.selectedBlock = null;
        document.getElementById('selected-block-info').style.display = 'none';
        this.visualizeMemory();
        this.refreshButtonStates();
    }

    handleCanvasKeydown(event) {
        if (!this.blocks || this.blocks.length === 0) return;

        const sorted = [...this.blocks].sort((a, b) => Number(a.address) - Number(b.address));
        const currentIndex = this.selectedBlock
            ? sorted.findIndex(b => b.address === this.selectedBlock.address)
            : -1;

        switch (event.key) {
            case 'ArrowRight':
            case 'ArrowDown': {
                event.preventDefault();
                const nextIdx = currentIndex < sorted.length - 1 ? currentIndex + 1 : 0;
                this.selectBlock(sorted[nextIdx]);
                this.announceBlock(sorted[nextIdx]);
                break;
            }
            case 'ArrowLeft':
            case 'ArrowUp': {
                event.preventDefault();
                const prevIdx = currentIndex > 0 ? currentIndex - 1 : sorted.length - 1;
                this.selectBlock(sorted[prevIdx]);
                this.announceBlock(sorted[prevIdx]);
                break;
            }
            case 'Enter':
            case ' ': {
                event.preventDefault();
                if (this.selectedBlock && this.selectedBlock.state === 1) {
                    this.deallocate();
                }
                break;
            }
            case 'Delete':
            case 'Backspace': {
                event.preventDefault();
                if (this.selectedBlock && this.selectedBlock.state === 1) {
                    this.deallocate();
                }
                break;
            }
            case 'Escape': {
                event.preventDefault();
                this.selectedBlock = null;
                document.getElementById('selected-block-info').style.display = 'none';
                this.visualizeMemory();
                this.refreshButtonStates();
                this.announce('Block deselected');
                break;
            }
        }
    }

    announceBlock(block) {
        const stateStr = block.state === 1 ? 'allocated' : 'free';
        const owner = block.state === 1 && block.owner ? ` owned by ${this.escapeHtml(block.owner)}` : '';
        this.announce(`Block ${block.id}: ${this.formatBytes(block.size)}, ${stateStr}${owner}, address 0x${Number(block.address).toString(16).toUpperCase()}`);
    }

    announce(message) {
        const sr = document.getElementById('block-list-sr');
        if (sr) {
            sr.textContent = message;
        }
    }

    selectBlock(block) {
        this.selectedBlock = { ...block, isAllocated: block.state === 1 };
        this.visualizeMemory();

        const infoPanel = document.getElementById('selected-block-info');
        const details = document.getElementById('block-details');
        const stateStr = block.state === 1 ? 'Allocated' : 'Free';
        const isAllocated = block.state === 1;

        details.innerHTML = `
            <strong>Block ID:</strong> ${block.id}<br>
            <strong>Address:</strong> 0x${Number(block.address).toString(16).toUpperCase()}<br>
            <strong>Size:</strong> ${this.formatBytes(block.size)}<br>
            <strong>State:</strong> ${stateStr}<br>
            ${isAllocated ? `<strong>Owner:</strong> ${this.escapeHtml(block.owner || 'Unknown')}<br>` : ''}
            ${isAllocated && block.allocatedAt ? `<strong>Allocated At:</strong> ${new Date(block.allocatedAt).toLocaleTimeString()}<br>` : ''}
        `;
        infoPanel.style.display = 'block';
        this.refreshButtonStates();
    }

    displayLeaks(leaks) {
        const leaksPanel = document.getElementById('leaks-panel');
        const leaksList = document.getElementById('leaks-list');
        if (!leaks || leaks.length === 0) {
            leaksPanel.style.display = 'none';
            return;
        }
        leaksPanel.style.display = 'block';
        leaksList.innerHTML = '';
        for (const leak of leaks) {
            const entry = document.createElement('div');
            entry.className = 'leak-entry';
            entry.innerHTML = `
                <strong>Block ${leak.blockId}</strong> (0x${Number(leak.address).toString(16).toUpperCase()}) -
                ${this.formatBytes(leak.size)} -
                Owner: ${this.escapeHtml(leak.owner || 'Unknown')} -
                Duration: ${((leak.duration || 0) / 1e9).toFixed(2)}s
            `;
            leaksList.appendChild(entry);
        }
    }

    escapeHtml(str) {
        return String(str)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    addLogEntry(message, type = 'info') {
        const log = document.getElementById('events-log');
        if (!log) return;
        const entry = document.createElement('div');
        entry.className = `log-entry ${type}`;
        entry.textContent = `[${new Date().toLocaleTimeString()}] ${message}`;
        log.insertBefore(entry, log.firstChild);
        while (log.children.length > 50) {
            log.removeChild(log.lastChild);
        }
    }

    updateStatus(text, connected) {
        const statusText = document.getElementById('status-text');
        statusText.textContent = text;
        statusText.style.color = connected ? '#48bb78' : '#f56565';
    }

    updateCanvasSize() {
        const container = document.getElementById('memory-canvas-container');
        this.canvas.width = Math.min(1200, container.clientWidth - 40);
        this.visualizeMemory();
    }

    formatBytes(bytes) {
        if (!bytes) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    formatNanoseconds(ns) {
        if (!ns) return '0 ns';
        if (ns < 1000) return `${ns} ns`;
        if (ns < 1000000) return `${(ns / 1000).toFixed(2)} μs`;
        return `${(ns / 1000000).toFixed(2)} ms`;
    }
}

document.addEventListener('DOMContentLoaded', () => {
    new MemorySimulator();
});
