// RVPodView - Podman Web Management

const App = {
    user: null,
    currentPage: 'dashboard',
    terminal: null,
    terminalSocket: null,
    terminalFitAddon: null,
    hostTerminal: null,
    hostTerminalSocket: null,
    hostTerminalFitAddon: null,
    autoRefreshIntervals: {},
    autoRefreshDelay: 5000, // 5 seconds

    // Authenticated fetch - handles 401 by redirecting to login
    async authFetch(url, options = {}) {
        const response = await fetch(url, options);
        if (response.status === 401) {
            // Session expired or logged out
            this.user = null;
            this.pauseAllAutoRefresh();
            document.getElementById('app').classList.add('hidden');
            document.getElementById('login-page').classList.remove('hidden');
            throw new Error('Session expired');
        }
        return response;
    },

    // Initialize application
    async init() {
        this.bindEvents();
        await this.checkAuth();
    },

    // Bind event listeners
    bindEvents() {
        // Login form
        document.getElementById('login-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.login();
        });

        // Logout button
        document.getElementById('logout-btn').addEventListener('click', () => this.logout());

        // Navigation
        document.querySelectorAll('.nav-item').forEach(item => {
            item.addEventListener('click', (e) => {
                e.preventDefault();
                this.navigateTo(item.dataset.page);
            });
        });

        // Dashboard
        document.getElementById('refresh-dashboard').addEventListener('click', () => this.loadDashboard());
        document.getElementById('auto-refresh-toggle').addEventListener('change', (e) => this.setAutoRefresh('dashboard', e.target.checked));
        document.getElementById('system-prune-btn').addEventListener('click', () => this.confirmAction('System Prune', 'This will remove all unused containers, images, and volumes. Are you sure?', () => this.systemPrune()));
        document.getElementById('system-reboot-btn').addEventListener('click', () => this.confirmAction('Reboot Host', 'Are you sure you want to reboot the host system? All containers will be stopped.', () => this.systemReboot()));
        document.getElementById('system-shutdown-btn').addEventListener('click', () => this.confirmAction('Shutdown Host', 'Are you sure you want to shutdown the host system? All containers will be stopped and the system will power off.', () => this.systemShutdown()));

        // Containers page
        document.getElementById('refresh-containers').addEventListener('click', () => this.loadContainers());
        document.getElementById('auto-refresh-containers').addEventListener('change', (e) => this.setAutoRefresh('containers', e.target.checked));
        document.getElementById('create-container-btn').addEventListener('click', () => this.showModal('modal-create-container'));
        document.getElementById('create-container-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.createContainer();
        });

        // Images page
        document.getElementById('refresh-images').addEventListener('click', () => this.loadImages());
        document.getElementById('auto-refresh-images').addEventListener('change', (e) => this.setAutoRefresh('images', e.target.checked));
        document.getElementById('pull-image-btn').addEventListener('click', () => this.showModal('modal-pull'));
        document.getElementById('pull-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.pullImage();
        });

        // Close dropdowns on click outside
        document.addEventListener('click', (e) => {
            if (!e.target.closest('.dropdown')) {
                document.querySelectorAll('.dropdown.open').forEach(d => d.classList.remove('open'));
            }
        });

        // Pause/resume auto-refresh when tab visibility changes
        document.addEventListener('visibilitychange', () => {
            if (document.hidden) {
                // Tab is hidden - pause all auto-refresh
                this.pauseAllAutoRefresh();
            } else {
                // Tab is visible - resume auto-refresh for current page (only if logged in)
                if (this.user) {
                    this.resumeAutoRefresh();
                }
            }
        });
    },

    // Check if user is authenticated
    async checkAuth() {
        try {
            const response = await fetch('/api/auth/me');
            if (response.ok) {
                const data = await response.json();
                this.user = data.user;
                this.showApp();
            } else {
                this.showLogin();
            }
        } catch {
            // Network error or server unavailable - silently show login
            this.showLogin();
        }
    },

    // Login
    async login() {
        const username = document.getElementById('username').value;
        const password = document.getElementById('password').value;
        const remember = document.getElementById('remember-me').checked;
        const errorEl = document.getElementById('login-error');

        try {
            const response = await fetch('/api/auth/login', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username, password, remember })
            });

            const data = await response.json();

            if (data.success) {
                this.user = data.user;
                errorEl.textContent = '';
                this.showApp();
            } else {
                errorEl.textContent = data.message || 'Login failed';
            }
        } catch (error) {
            errorEl.textContent = 'Connection error';
        }
    },

    // Logout
    async logout() {
        try {
            await fetch('/api/auth/logout', { method: 'POST' });
        } catch (e) {}
        this.user = null;
        this.showLogin();
    },

    // Show login page
    showLogin() {
        document.getElementById('login-page').classList.remove('hidden');
        document.getElementById('app').classList.add('hidden');
        document.body.classList.remove('is-admin');
        // Clear login form
        document.getElementById('username').value = '';
        document.getElementById('password').value = '';
        document.getElementById('login-error').textContent = '';
    },

    // Show main app
    showApp() {
        document.getElementById('login-page').classList.add('hidden');
        document.getElementById('app').classList.remove('hidden');

        // Update user info
        document.getElementById('current-user').textContent = this.user.username;
        const roleEl = document.getElementById('user-role');
        roleEl.textContent = this.user.role;
        roleEl.className = 'badge ' + this.user.role;

        // Set version fallback for dev builds
        const versionEl = document.querySelector('.app-version');
        if (versionEl && versionEl.textContent === '{{VERSION}}') {
            versionEl.textContent = 'dev';
        }

        // Set admin class for showing admin-only elements
        if (this.user.role === 'admin') {
            document.body.classList.add('is-admin');
        } else {
            document.body.classList.remove('is-admin');
        }

        // Load initial page
        this.navigateTo('dashboard');
    },

    // Navigate to page
    navigateTo(page) {
        // Cleanup previous page resources
        if (this.currentPage === 'terminal') {
            this.cleanupHostTerminal();
        }
        // Stop auto-refresh on previous page
        this.stopAutoRefresh(this.currentPage);

        this.currentPage = page;

        // Update nav
        document.querySelectorAll('.nav-item').forEach(item => {
            item.classList.toggle('active', item.dataset.page === page);
        });

        // Show page
        document.querySelectorAll('.content-page').forEach(p => {
            p.classList.add('hidden');
        });
        document.getElementById('page-' + page).classList.remove('hidden');

        // Load page data and restore auto-refresh
        switch (page) {
            case 'dashboard':
                this.loadDashboard();
                this.restoreAutoRefresh('dashboard');
                break;
            case 'containers':
                this.loadContainers();
                this.restoreAutoRefresh('containers');
                break;
            case 'images':
                this.loadImages();
                this.restoreAutoRefresh('images');
                break;
            case 'terminal':
                this.initHostTerminal();
                break;
        }
    },

    // Cleanup when leaving terminal page
    cleanupHostTerminal() {
        if (this.hostTerminalSocket) {
            this.hostTerminalSocket.close();
            this.hostTerminalSocket = null;
        }
        if (this.hostTerminal) {
            this.hostTerminal.dispose();
            this.hostTerminal = null;
        }
        this.hostTerminalFitAddon = null;
    },

    // Initialize host terminal
    initHostTerminal() {
        const container = document.getElementById('host-terminal-container');
        container.innerHTML = '';

        // Check if xterm is available
        if (typeof Terminal === 'undefined') {
            container.innerHTML = '<p style="color: #d4d4d4; padding: 20px;">Terminal library not loaded. Please check your internet connection.</p>';
            return;
        }

        // Create terminal
        this.hostTerminal = new Terminal({
            cursorBlink: true,
            fontSize: 14,
            fontFamily: '"Consolas", "Monaco", monospace',
            theme: {
                background: '#1e1e1e',
                foreground: '#d4d4d4'
            }
        });

        // Add fit addon if available
        if (typeof FitAddon !== 'undefined') {
            this.hostTerminalFitAddon = new FitAddon.FitAddon();
            this.hostTerminal.loadAddon(this.hostTerminalFitAddon);
        }

        this.hostTerminal.open(container);

        if (this.hostTerminalFitAddon) {
            this.hostTerminalFitAddon.fit();
        }

        // Connect WebSocket
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/terminal`;

        this.hostTerminal.writeln('Connecting to host...\r\n');

        try {
            this.hostTerminalSocket = new WebSocket(wsUrl);

            this.hostTerminalSocket.onopen = () => {
                this.hostTerminal.writeln('\x1b[32mConnected!\x1b[0m\r\n');
                // Send initial resize
                if (this.hostTerminalFitAddon) {
                    const dims = this.hostTerminalFitAddon.proposeDimensions();
                    if (dims) {
                        this.hostTerminalSocket.send(JSON.stringify({
                            type: 'resize',
                            cols: dims.cols,
                            rows: dims.rows
                        }));
                    }
                }
            };

            this.hostTerminalSocket.onmessage = (event) => {
                this.hostTerminal.write(event.data);
            };

            this.hostTerminalSocket.onclose = () => {
                if (this.hostTerminal) {
                    this.hostTerminal.writeln('\r\n\x1b[31mConnection closed\x1b[0m');
                }
            };

            this.hostTerminalSocket.onerror = (error) => {
                if (this.hostTerminal) {
                    this.hostTerminal.writeln('\r\n\x1b[31mConnection error\x1b[0m');
                }
                console.error('WebSocket error:', error);
            };

            // Send input to WebSocket
            this.hostTerminal.onData(data => {
                if (this.hostTerminalSocket && this.hostTerminalSocket.readyState === WebSocket.OPEN) {
                    this.hostTerminalSocket.send(JSON.stringify({ type: 'stdin', data: data }));
                }
            });

            // Handle resize
            window.addEventListener('resize', () => {
                if (this.hostTerminalFitAddon && this.hostTerminalSocket && this.hostTerminalSocket.readyState === WebSocket.OPEN) {
                    this.hostTerminalFitAddon.fit();
                    const dims = this.hostTerminalFitAddon.proposeDimensions();
                    if (dims) {
                        this.hostTerminalSocket.send(JSON.stringify({
                            type: 'resize',
                            cols: dims.cols,
                            rows: dims.rows
                        }));
                    }
                }
            });

        } catch (error) {
            this.hostTerminal.writeln('\r\n\x1b[31mFailed to connect: ' + error.message + '\x1b[0m');
        }
    },

    // Auto-refresh configuration per page
    autoRefreshConfig: {
        dashboard: { toggle: 'auto-refresh-toggle', button: 'refresh-dashboard', loader: 'loadDashboard' },
        containers: { toggle: 'auto-refresh-containers', button: 'refresh-containers', loader: 'loadContainers' },
        images: { toggle: 'auto-refresh-images', button: 'refresh-images', loader: 'loadImages' }
    },

    // Set auto-refresh for a page
    setAutoRefresh(page, enabled) {
        const config = this.autoRefreshConfig[page];
        if (!config) return;

        const refreshBtn = document.getElementById(config.button);

        // Save to localStorage
        localStorage.setItem('autoRefresh_' + page, enabled ? '1' : '0');

        // Clear existing interval first
        if (this.autoRefreshIntervals[page]) {
            clearInterval(this.autoRefreshIntervals[page]);
            delete this.autoRefreshIntervals[page];
        }

        if (enabled) {
            refreshBtn.disabled = true;
            this[config.loader]();
            this.autoRefreshIntervals[page] = setInterval(() => {
                if (this.currentPage === page && !document.hidden) {
                    this[config.loader]();
                }
            }, this.autoRefreshDelay);
        } else {
            refreshBtn.disabled = false;
        }
    },

    // Stop auto-refresh for a page (when navigating away)
    stopAutoRefresh(page) {
        // Just clear the interval, don't change localStorage or button state
        if (this.autoRefreshIntervals[page]) {
            clearInterval(this.autoRefreshIntervals[page]);
            delete this.autoRefreshIntervals[page];
        }
    },

    // Restore auto-refresh state from localStorage (when navigating to page)
    restoreAutoRefresh(page) {
        const config = this.autoRefreshConfig[page];
        if (!config) return;

        const saved = localStorage.getItem('autoRefresh_' + page);
        const toggle = document.getElementById(config.toggle);
        const refreshBtn = document.getElementById(config.button);

        if (saved === '1') {
            if (toggle) toggle.checked = true;
            if (refreshBtn) refreshBtn.disabled = true;
            this.startAutoRefreshInterval(page);
        } else {
            if (toggle) toggle.checked = false;
            if (refreshBtn) refreshBtn.disabled = false;
        }
    },

    // Start auto-refresh interval for a page
    startAutoRefreshInterval(page) {
        const config = this.autoRefreshConfig[page];
        if (!config) return;

        // Clear existing interval first
        if (this.autoRefreshIntervals[page]) {
            clearInterval(this.autoRefreshIntervals[page]);
        }

        this.autoRefreshIntervals[page] = setInterval(() => {
            if (this.currentPage === page && !document.hidden) {
                this[config.loader]();
            }
        }, this.autoRefreshDelay);
    },

    // Pause all auto-refresh intervals (when tab is hidden)
    pauseAllAutoRefresh() {
        Object.keys(this.autoRefreshIntervals).forEach(page => {
            if (this.autoRefreshIntervals[page]) {
                clearInterval(this.autoRefreshIntervals[page]);
                delete this.autoRefreshIntervals[page];
            }
        });
    },

    // Resume auto-refresh for current page (when tab becomes visible)
    resumeAutoRefresh() {
        const page = this.currentPage;
        const config = this.autoRefreshConfig[page];
        if (!config) return;

        const saved = localStorage.getItem('autoRefresh_' + page);
        if (saved === '1') {
            // Reload data immediately and restart interval
            this[config.loader]();
            this.startAutoRefreshInterval(page);
        }
    },

    // Load dashboard data
    async loadDashboard() {
        try {
            const response = await this.authFetch('/api/system/dashboard');
            if (!response.ok) throw new Error('Failed to load dashboard');

            const data = await response.json();

            // Update stats
            document.getElementById('stat-containers').textContent = data.containers.total;
            document.getElementById('stat-running').textContent = data.containers.running + ' running';
            document.getElementById('stat-stopped').textContent = data.containers.stopped + ' stopped';
            document.getElementById('stat-images').textContent = data.images;
            document.getElementById('stat-volumes').textContent = data.volumes;
            document.getElementById('stat-networks').textContent = data.networks;

            // Update system info
            if (data.system && data.system.host) {
                document.getElementById('info-hostname').textContent = data.system.host.hostname || '-';
                document.getElementById('info-kernel').textContent = data.system.host.kernel || '-';
                document.getElementById('info-arch').textContent = data.system.host.arch || '-';
            }
            if (data.system && data.system.version) {
                document.getElementById('info-podman').textContent = data.system.version.Version || '-';
            }
            if (data.system && data.system.host && data.system.host.memTotal) {
                const memTotal = this.formatBytes(data.system.host.memTotal);
                const memFree = this.formatBytes(data.system.host.memFree);
                document.getElementById('info-memory').textContent = `${memFree} free / ${memTotal} total`;
            }

            // Update host stats (CPU, uptime, disk, temperatures)
            if (data.hostStats) {
                document.getElementById('info-cpu').textContent = data.hostStats.cpuUsage.toFixed(1) + '%';
                document.getElementById('info-uptime').textContent = this.formatUptime(data.hostStats.uptime);

                // Update disk
                if (data.hostStats.diskTotal) {
                    const used = data.hostStats.diskTotal - data.hostStats.diskFree;
                    const usedStr = this.formatBytes(used);
                    const totalStr = this.formatBytes(data.hostStats.diskTotal);
                    document.getElementById('info-disk').textContent = `${usedStr} / ${totalStr}`;
                }

                // Update temperatures
                const tempsGrid = document.getElementById('temps-grid');
                if (data.hostStats.temperatures && data.hostStats.temperatures.length > 0) {
                    tempsGrid.innerHTML = data.hostStats.temperatures.map(t => {
                        // Temperature thresholds for Orange Pi RV2 (SpacemiT K1)
                        // < 50°C = cool (excellent), 50-65°C = normal, 65-75°C = warm, 75-85°C = hot, > 85°C = critical
                        let tempClass = 'normal';
                        if (t.temp < 50) tempClass = 'cool';
                        else if (t.temp < 65) tempClass = 'normal';
                        else if (t.temp < 75) tempClass = 'warm';
                        else if (t.temp < 85) tempClass = 'hot';
                        else tempClass = 'critical';
                        return `
                            <div class="temp-item">
                                <span class="temp-label">${t.label}</span>
                                <span class="temp-value ${tempClass}">${t.temp.toFixed(1)}°C</span>
                            </div>
                        `;
                    }).join('');
                } else {
                    tempsGrid.innerHTML = '<span class="info-value">No sensors found</span>';
                }
            }
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to load dashboard', 'error');
        }
    },

    // Load containers list
    async loadContainers() {
        const tbody = document.getElementById('containers-list');
        const existingRows = tbody.querySelectorAll('tr[data-id]');
        const isInitialLoad = existingRows.length === 0;

        if (isInitialLoad) {
            tbody.innerHTML = '<tr><td colspan="5">Loading...</td></tr>';
        }

        try {
            const response = await this.authFetch('/api/containers');
            if (!response.ok) throw new Error('Failed to load containers');

            const containers = await response.json();

            if (!containers || containers.length === 0) {
                tbody.innerHTML = '<tr><td colspan="5">No containers found</td></tr>';
                return;
            }

            const newIds = new Set(containers.map(c => c.Id || c.ID));
            const existingIds = new Set([...existingRows].map(r => r.dataset.id));

            // Update existing rows or add new ones
            containers.forEach(c => {
                const id = c.Id || c.ID;
                const existingRow = tbody.querySelector(`tr[data-id="${id}"]`);

                if (existingRow) {
                    // Update only dynamic data (state, stats)
                    const statusCell = existingRow.querySelector('.status');
                    const statsCell = existingRow.querySelector('.stats-cell');
                    const actionsCell = existingRow.querySelector('.actions');

                    const newState = c.State;
                    const oldState = statusCell.textContent;

                    if (oldState !== newState) {
                        statusCell.className = `status ${newState}`;
                        statusCell.textContent = newState;
                        // Update actions menu when state changes
                        actionsCell.innerHTML = this.getContainerActions(c);
                    }

                    const statsDisplay = c.State === 'running'
                        ? `${c.CPU.toFixed(1)}% / ${this.formatBytes(c.MemUsage)}`
                        : '-';
                    statsCell.textContent = statsDisplay;
                } else {
                    // Add new row
                    const tr = document.createElement('tr');
                    tr.dataset.id = id;
                    tr.innerHTML = this.getContainerRowContent(c);
                    tbody.appendChild(tr);
                }
            });

            // Remove rows for deleted containers
            existingRows.forEach(row => {
                if (!newIds.has(row.dataset.id)) {
                    row.remove();
                }
            });

            // On initial load, rebuild all rows with data-id
            if (isInitialLoad) {
                tbody.innerHTML = containers.map(c => {
                    const id = c.Id || c.ID;
                    return `<tr data-id="${id}">${this.getContainerRowContent(c)}</tr>`;
                }).join('');
            }
        } catch (error) {
            if (error.message !== 'Session expired') {
                if (isInitialLoad) {
                    tbody.innerHTML = '<tr><td colspan="5">Error loading containers</td></tr>';
                }
                this.showToast('Failed to load containers', 'error');
            }
        }
    },

    // Get container row HTML content (without tr wrapper)
    getContainerRowContent(c) {
        const statsDisplay = c.State === 'running'
            ? `${c.CPU.toFixed(1)}% / ${this.formatBytes(c.MemUsage)}`
            : '-';

        return `
            <td class="truncate">${this.getContainerName(c)}</td>
            <td class="truncate">${c.Image}</td>
            <td><span class="status ${c.State}">${c.State}</span></td>
            <td class="stats-cell">${statsDisplay}</td>
            <td class="actions">
                ${this.getContainerActions(c)}
            </td>`;
    },

    // Get container name from Names array
    getContainerName(container) {
        if (container.Names && container.Names.length > 0) {
            return container.Names[0].replace(/^\//, '');
        }
        const id = container.Id || container.ID || '';
        return id.substring(0, 12);
    },

    // Get container action buttons
    getContainerActions(container) {
        const isAdmin = this.user && this.user.role === 'admin';
        const id = container.Id || container.ID;

        let menuItems = `<button class="dropdown-item" onclick="App.viewLogs('${id}')">Logs</button>`;

        if (isAdmin) {
            if (container.State === 'running') {
                menuItems += `<button class="dropdown-item" onclick="App.openTerminal('${id}')">Terminal</button>`;
                menuItems += `<div class="dropdown-divider"></div>`;
                menuItems += `<button class="dropdown-item" onclick="App.stopContainer('${id}')">Stop</button>`;
                menuItems += `<button class="dropdown-item" onclick="App.restartContainer('${id}')">Restart</button>`;
            } else {
                menuItems += `<div class="dropdown-divider"></div>`;
                menuItems += `<button class="dropdown-item" onclick="App.startContainer('${id}')">Start</button>`;
            }
            menuItems += `<div class="dropdown-divider"></div>`;
            menuItems += `<button class="dropdown-item btn-danger" onclick="App.removeContainer('${id}')">Remove</button>`;
        }

        return `
            <div class="dropdown">
                <button class="btn btn-small btn-icon-only" onclick="App.toggleDropdown(this)">
                    <svg viewBox="0 0 24 24" fill="currentColor" width="16" height="16">
                        <circle cx="12" cy="5" r="2"/>
                        <circle cx="12" cy="12" r="2"/>
                        <circle cx="12" cy="19" r="2"/>
                    </svg>
                </button>
                <div class="dropdown-menu">
                    ${menuItems}
                </div>
            </div>`;
    },

    // Toggle dropdown menu
    toggleDropdown(btn) {
        const dropdown = btn.parentElement;
        const menu = dropdown.querySelector('.dropdown-menu');
        const isOpen = dropdown.classList.contains('open');

        // Close all dropdowns
        document.querySelectorAll('.dropdown.open').forEach(d => {
            d.classList.remove('open');
        });

        // Toggle current
        if (!isOpen) {
            dropdown.classList.add('open');

            // Position the menu
            const btnRect = btn.getBoundingClientRect();
            const menuHeight = menu.offsetHeight || 200;
            const spaceBelow = window.innerHeight - btnRect.bottom;

            menu.style.right = (window.innerWidth - btnRect.right) + 'px';

            // Show above or below depending on space
            if (spaceBelow < menuHeight && btnRect.top > menuHeight) {
                menu.style.top = 'auto';
                menu.style.bottom = (window.innerHeight - btnRect.top) + 'px';
            } else {
                menu.style.top = btnRect.bottom + 'px';
                menu.style.bottom = 'auto';
            }
        }
    },

    // Container actions
    async startContainer(id) {
        this.showToast('Starting container...', 'info');
        try {
            const response = await this.authFetch(`/api/containers/${id}/start`, { method: 'POST' });
            if (!response.ok) throw new Error('Failed to start container');
            this.showToast('Container started', 'success');
            this.loadContainers();
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to start container', 'error');
        }
    },

    async stopContainer(id) {
        this.showToast('Stopping container...', 'info');
        try {
            const response = await this.authFetch(`/api/containers/${id}/stop`, { method: 'POST' });
            if (!response.ok) throw new Error('Failed to stop container');
            this.showToast('Container stopped', 'success');
            this.loadContainers();
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to stop container', 'error');
        }
    },

    async restartContainer(id) {
        this.showToast('Restarting container...', 'info');
        try {
            const response = await this.authFetch(`/api/containers/${id}/restart`, { method: 'POST' });
            if (!response.ok) throw new Error('Failed to restart container');
            this.showToast('Container restarted', 'success');
            this.loadContainers();
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to restart container', 'error');
        }
    },

    removeContainer(id) {
        this.confirmAction('Remove Container', 'Are you sure you want to remove this container?', async () => {
            this.showToast('Removing container...', 'info');
            try {
                const response = await this.authFetch(`/api/containers/${id}?force=true`, { method: 'DELETE' });
                if (!response.ok) throw new Error('Failed to remove container');
                this.showToast('Container removed', 'success');
                this.loadContainers();
            } catch (error) {
                if (error.message !== 'Session expired') this.showToast('Failed to remove container', 'error');
            }
        });
    },

    async viewLogs(id) {
        const logsContent = document.getElementById('logs-content');
        logsContent.textContent = 'Loading...';
        this.showModal('modal-logs');

        try {
            const response = await this.authFetch(`/api/containers/${id}/logs?tail=200`);
            if (!response.ok) throw new Error('Failed to load logs');
            const data = await response.json();
            logsContent.textContent = data.logs || 'No logs available';
        } catch (error) {
            if (error.message !== 'Session expired') logsContent.textContent = 'Error loading logs';
        }
    },

    // Load images list
    async loadImages() {
        const tbody = document.getElementById('images-list');
        const existingRows = tbody.querySelectorAll('tr[data-id]');
        const isInitialLoad = existingRows.length === 0;

        if (isInitialLoad) {
            tbody.innerHTML = '<tr><td colspan="7">Loading...</td></tr>';
        }

        try {
            const response = await this.authFetch('/api/images');
            if (!response.ok) throw new Error('Failed to load images');

            const images = await response.json();

            if (!images || images.length === 0) {
                tbody.innerHTML = '<tr><td colspan="7">No images found</td></tr>';
                return;
            }

            const newIds = new Set(images.map(img => img.Id || img.ID));

            // Update existing rows or add new ones
            images.forEach(img => {
                const imgId = img.Id || img.ID || '';
                const existingRow = tbody.querySelector(`tr[data-id="${imgId}"]`);

                if (existingRow) {
                    // Update only InUse status (the only thing that can change for images)
                    const statusCell = existingRow.querySelector('.badge');
                    if (statusCell) {
                        const isInUse = statusCell.classList.contains('in-use');
                        if (isInUse !== img.InUse) {
                            statusCell.className = img.InUse ? 'badge in-use' : 'badge unused';
                            statusCell.textContent = img.InUse ? 'In Use' : 'Unused';
                        }
                    }
                } else {
                    // Add new row
                    const tr = document.createElement('tr');
                    tr.dataset.id = imgId;
                    tr.innerHTML = this.getImageRowContent(img);
                    tbody.appendChild(tr);
                }
            });

            // Remove rows for deleted images
            existingRows.forEach(row => {
                if (!newIds.has(row.dataset.id)) {
                    row.remove();
                }
            });

            // On initial load, rebuild all rows with data-id
            if (isInitialLoad) {
                tbody.innerHTML = images.map(img => {
                    const imgId = img.Id || img.ID || '';
                    return `<tr data-id="${imgId}">${this.getImageRowContent(img)}</tr>`;
                }).join('');
            }
        } catch (error) {
            if (error.message !== 'Session expired') {
                if (isInitialLoad) {
                    tbody.innerHTML = '<tr><td colspan="7">Error loading images</td></tr>';
                }
                this.showToast('Failed to load images', 'error');
            }
        }
    },

    // Get image row HTML content (without tr wrapper)
    getImageRowContent(img) {
        const [repo, tag] = this.parseImageTag(img);
        const imgId = img.Id || img.ID || '';
        const shortId = imgId.substring(0, 12);
        const displayRepo = repo === '<none>' ? `<span class="text-muted">&lt;none&gt;</span>` : repo;
        const displayTag = tag === '<none>' ? `<span class="text-muted">&lt;none&gt;</span>` : tag;
        const usageStatus = img.InUse
            ? '<span class="badge in-use">In Use</span>'
            : '<span class="badge unused">Unused</span>';

        return `
            <td class="truncate">${displayRepo}</td>
            <td>${displayTag}</td>
            <td class="id-short">${shortId}</td>
            <td>${this.formatBytes(img.Size)}</td>
            <td>${this.formatDate(img.Created)}</td>
            <td>${usageStatus}</td>
            <td class="actions">
                ${this.user && this.user.role === 'admin'
                    ? `<div class="dropdown">
                        <button class="btn btn-small" onclick="App.toggleDropdown(this)">...</button>
                        <div class="dropdown-menu">
                            <button class="dropdown-item btn-danger" onclick="App.removeImage('${imgId}')">Remove</button>
                        </div>
                    </div>`
                    : ''}
            </td>`;
    },

    // Parse image repository and tag
    parseImageTag(image) {
        if (image.RepoTags && image.RepoTags.length > 0 && image.RepoTags[0] !== '<none>:<none>') {
            const parts = image.RepoTags[0].split(':');
            const tag = parts.pop();
            return [parts.join(':'), tag];
        }
        return ['<none>', '<none>'];
    },

    // Pull image
    async pullImage() {
        const reference = document.getElementById('image-reference').value;
        if (!reference) return;

        const btn = document.querySelector('#pull-form button[type="submit"]');
        btn.disabled = true;
        btn.textContent = 'Pulling...';
        this.showToast('Pulling image...', 'info');

        try {
            const response = await this.authFetch('/api/images/pull', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ reference })
            });

            if (!response.ok) throw new Error('Failed to pull image');

            this.showToast('Image pulled successfully', 'success');
            this.closeModal('modal-pull');
            this.loadImages();
            document.getElementById('image-reference').value = '';
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to pull image', 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = 'Pull';
        }
    },

    // Remove image
    removeImage(id) {
        this.confirmAction('Remove Image', 'Are you sure you want to remove this image?', async () => {
            this.showToast('Removing image...', 'info');
            try {
                const response = await this.authFetch(`/api/images/${id}?force=true`, { method: 'DELETE' });
                if (!response.ok) throw new Error('Failed to remove image');
                this.showToast('Image removed', 'success');
                this.loadImages();
            } catch (error) {
                if (error.message !== 'Session expired') this.showToast('Failed to remove image', 'error');
            }
        });
    },

    // System prune
    async systemPrune() {
        const btn = document.getElementById('system-prune-btn');
        btn.disabled = true;
        btn.textContent = 'Cleaning...';
        this.showToast('Cleaning system...', 'info');

        try {
            const response = await this.authFetch('/api/system/prune', { method: 'POST' });
            if (!response.ok) throw new Error('Failed to prune system');
            this.showToast('System cleaned successfully', 'success');
            this.loadDashboard();
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to prune system', 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = 'System Prune';
        }
    },

    // Confirm action modal
    confirmAction(title, message, callback) {
        document.getElementById('confirm-title').textContent = title;
        document.getElementById('confirm-message').textContent = message;
        const actionBtn = document.getElementById('confirm-action-btn');
        actionBtn.onclick = () => {
            this.closeModal('modal-confirm');
            callback();
        };
        this.showModal('modal-confirm');
    },

    // System reboot
    async systemReboot() {
        const btn = document.getElementById('system-reboot-btn');
        btn.disabled = true;
        btn.textContent = 'Rebooting...';

        try {
            const response = await this.authFetch('/api/system/reboot', { method: 'POST' });
            if (!response.ok) throw new Error('Failed to reboot');
            this.showToast('System is rebooting...', 'success');
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to reboot system', 'error');
            btn.disabled = false;
            btn.textContent = 'Reboot';
        }
    },

    // System shutdown
    async systemShutdown() {
        const btn = document.getElementById('system-shutdown-btn');
        btn.disabled = true;
        btn.textContent = 'Shutting down...';

        try {
            const response = await this.authFetch('/api/system/shutdown', { method: 'POST' });
            if (!response.ok) throw new Error('Failed to shutdown');
            this.showToast('System is shutting down...', 'success');
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast('Failed to shutdown system', 'error');
            btn.disabled = false;
            btn.textContent = 'Shutdown';
        }
    },

    // Create container
    async createContainer() {
        const form = document.getElementById('create-container-form');
        const btn = form.querySelector('button[type="submit"]');
        btn.disabled = true;
        btn.textContent = 'Creating...';
        this.showToast('Creating container...', 'info');

        const data = {
            image: document.getElementById('container-image').value,
            name: document.getElementById('container-name').value,
            ports: document.getElementById('container-ports').value,
            volumes: document.getElementById('container-volumes').value,
            env: document.getElementById('container-env').value,
            command: document.getElementById('container-command').value,
            start: document.getElementById('container-start').checked
        };

        try {
            const response = await this.authFetch('/api/containers', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(data)
            });

            const result = await response.json();

            if (!response.ok) {
                throw new Error(result.error || 'Failed to create container');
            }

            this.showToast(`Container ${result.status}`, 'success');
            this.closeModal('modal-create-container');
            form.reset();
            this.loadContainers();
        } catch (error) {
            if (error.message !== 'Session expired') this.showToast(error.message, 'error');
        } finally {
            btn.disabled = false;
            btn.textContent = 'Create';
        }
    },

    // Open terminal
    openTerminal(containerId) {
        this.showModal('modal-terminal');
        const container = document.getElementById('terminal-container');
        container.innerHTML = '';

        // Check if xterm is available
        if (typeof Terminal === 'undefined') {
            container.innerHTML = '<p style="color: #d4d4d4; padding: 20px;">Terminal library not loaded. Please check your internet connection.</p>';
            return;
        }

        // Create terminal
        this.terminal = new Terminal({
            cursorBlink: true,
            fontSize: 14,
            fontFamily: '"Consolas", "Monaco", monospace',
            theme: {
                background: '#1e1e1e',
                foreground: '#d4d4d4'
            }
        });

        // Add fit addon if available
        if (typeof FitAddon !== 'undefined') {
            this.terminalFitAddon = new FitAddon.FitAddon();
            this.terminal.loadAddon(this.terminalFitAddon);
        }

        this.terminal.open(container);

        if (this.terminalFitAddon) {
            this.terminalFitAddon.fit();
        }

        // Connect WebSocket
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/containers/${containerId}/terminal`;

        this.terminal.writeln('Connecting to container...');

        try {
            this.terminalSocket = new WebSocket(wsUrl);

            this.terminalSocket.onopen = () => {
                this.terminal.writeln('Connected!\r\n');
            };

            this.terminalSocket.onmessage = (event) => {
                this.terminal.write(event.data);
            };

            this.terminalSocket.onclose = () => {
                this.terminal.writeln('\r\n\x1b[31mConnection closed\x1b[0m');
            };

            this.terminalSocket.onerror = (error) => {
                this.terminal.writeln('\r\n\x1b[31mConnection error\x1b[0m');
                console.error('WebSocket error:', error);
            };

            // Send input to WebSocket
            this.terminal.onData(data => {
                if (this.terminalSocket && this.terminalSocket.readyState === WebSocket.OPEN) {
                    this.terminalSocket.send(JSON.stringify({ type: 'stdin', data: data }));
                }
            });

        } catch (error) {
            this.terminal.writeln('\r\n\x1b[31mFailed to connect: ' + error.message + '\x1b[0m');
        }
    },

    // Close terminal
    closeTerminal() {
        if (this.terminalSocket) {
            this.terminalSocket.close();
            this.terminalSocket = null;
        }
        if (this.terminal) {
            this.terminal.dispose();
            this.terminal = null;
        }
        this.terminalFitAddon = null;
        this.closeModal('modal-terminal');
    },

    // Modal helpers
    showModal(id) {
        document.getElementById(id).classList.remove('hidden');
    },

    closeModal(id) {
        document.getElementById(id).classList.add('hidden');
    },

    // Toast notifications
    showToast(message, type = 'info') {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;
        toast.textContent = message;
        container.appendChild(toast);

        setTimeout(() => {
            toast.remove();
        }, 3000);
    },

    // Format helpers
    formatBytes(bytes) {
        if (!bytes) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let i = 0;
        while (bytes >= 1024 && i < units.length - 1) {
            bytes /= 1024;
            i++;
        }
        return `${bytes.toFixed(1)} ${units[i]}`;
    },

    formatDate(timestamp) {
        if (!timestamp) return '-';
        const date = new Date(timestamp * 1000);
        return date.toLocaleDateString() + ' ' + date.toLocaleTimeString();
    },

    formatUptime(seconds) {
        if (!seconds) return '-';
        const days = Math.floor(seconds / 86400);
        const hours = Math.floor((seconds % 86400) / 3600);
        const minutes = Math.floor((seconds % 3600) / 60);

        let parts = [];
        if (days > 0) parts.push(`${days}d`);
        if (hours > 0) parts.push(`${hours}h`);
        if (minutes > 0 || parts.length === 0) parts.push(`${minutes}m`);

        return parts.join(' ');
    }
};

// Global function for modal close
function closeModal(id) {
    App.closeModal(id);
}

// Initialize app when DOM is ready
document.addEventListener('DOMContentLoaded', () => App.init());

// Register Service Worker for PWA
if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
        navigator.serviceWorker.register('/static/sw.js')
            .then((registration) => {
                console.log('SW registered:', registration.scope);
            })
            .catch((error) => {
                console.log('SW registration failed:', error);
            });
    });
}
