// PodmanView - Podman Web Management

// User roles (enum-like constants)
const UserRole = Object.freeze({
    ADMIN: 'admin',
    READONLY: 'readonly'
});

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
    xtermLoaded: false,
    xtermLoading: false,
    logsContainerId: null,
    logsAutoInterval: null,
    eventsLastId: 0,
    eventsOpen: false,
    eventsCheckInterval: null,

    // Command history for terminal
    commandHistory: [],
    historyIndex: -1,
    currentLine: '',
    savedLine: '',

    // Terminal reconnection state
    hostTerminalReconnecting: false,
    hostTerminalReconnectAttempts: 0,
    hostTerminalReconnectTimer: null,
    containerTerminalReconnecting: false,
    containerTerminalReconnectAttempts: 0,
    containerTerminalReconnectTimer: null,
    currentContainerId: null,

    // File Manager state
    fileManagerCurrentPath: '/',
    fileManagerFiles: [],
    fileManagerParent: '/',
    fileEditorPath: null,
    fileEditorOriginalContent: null,
    fileManagerOffset: 0,
    fileManagerLimit: 500,
    fileManagerTotalCount: 0,
    fileManagerHasMore: false,

    // File Manager lazy loading state
    fileManagerCoreLoaded: false,
    fileManagerCoreLoading: null, // Promise or null
    fileViewersLoaded: {}, // Track loaded viewers: { TextViewer: true, ImageViewer: false, ... }
    fileViewersLoading: {}, // Track loading viewers: { TextViewer: Promise, ... }

    // File cache (LRU cache for recently opened files)
    fileCache: new Map(), // path -> { content, mimeType, encoding, size, timestamp }
    fileCacheMaxSize: 10 * 1024 * 1024, // 10MB max total cache size
    fileCacheCurrentSize: 0,

    // Lazy load File Manager core modules (bundled) - Promise-based (optimized!)
    async loadFileManagerCore() {
        if (this.fileManagerCoreLoaded) return true;

        // If already loading, return the existing Promise
        if (this.fileManagerCoreLoading) {
            return await this.fileManagerCoreLoading;
        }

        // Create and store Promise for concurrent requests
        this.fileManagerCoreLoading = (async () => {
            try {
                console.log('[ModuleLoader] Loading File Manager core bundle...');
                const startTime = performance.now();

                // Load bundled core (IconHelper + FileTypeDetector + BaseFileViewer + FileViewerFactory)
                // Replaces 4 HTTP requests with 1
                await this.loadScript('/static/js/file-manager-core.js');

                const loadTime = Math.round(performance.now() - startTime);
                console.log(`[ModuleLoader] File Manager core bundle loaded in ${loadTime}ms`);

                this.fileManagerCoreLoaded = true;
                return true;
            } catch (err) {
                console.error('[ModuleLoader] Failed to load File Manager core:', err);
                this.showToast('Failed to load File Manager', 'error');
                return false;
            } finally {
                this.fileManagerCoreLoading = null;
            }
        })();

        return await this.fileManagerCoreLoading;
    },

    // Lazy load specific file viewer
    async loadFileViewer(viewerName) {
        // Check if already loaded
        if (this.fileViewersLoaded[viewerName]) {
            return true;
        }

        // Check if currently loading
        if (this.fileViewersLoading[viewerName]) {
            return await this.fileViewersLoading[viewerName];
        }

        // Map viewer names to script paths
        const viewerScripts = {
            'TextViewer': '/static/js/viewers/TextViewer.js',
            'ImageViewer': '/static/js/viewers/ImageViewer.js',
            'VideoViewer': '/static/js/viewers/VideoViewer.js',
            'AudioViewer': '/static/js/viewers/AudioViewer.js',
            'PDFViewer': '/static/js/viewers/PDFViewer.js'
        };

        const scriptPath = viewerScripts[viewerName];
        if (!scriptPath) {
            console.warn(`[ModuleLoader] Unknown viewer: ${viewerName}`);
            return false;
        }

        // Start loading
        const loadPromise = (async () => {
            try {
                console.log(`[ModuleLoader] Loading ${viewerName}...`);
                const startTime = performance.now();

                await this.loadScript(scriptPath);

                const loadTime = Math.round(performance.now() - startTime);
                console.log(`[ModuleLoader] ${viewerName} loaded in ${loadTime}ms`);

                this.fileViewersLoaded[viewerName] = true;
                delete this.fileViewersLoading[viewerName];
                return true;
            } catch (err) {
                console.error(`[ModuleLoader] Failed to load ${viewerName}:`, err);
                delete this.fileViewersLoading[viewerName];
                return false;
            }
        })();

        this.fileViewersLoading[viewerName] = loadPromise;
        return await loadPromise;
    },

    // Detect and load required viewer for file type
    async loadViewerForFileType(fileType) {
        // Map file categories to viewer names
        const viewerMap = {
            'text': 'TextViewer',
            'code': 'TextViewer',
            'markdown': 'TextViewer',
            'image': 'ImageViewer',
            'video': 'VideoViewer',
            'audio': 'AudioViewer',
            'pdf': 'PDFViewer'
        };

        const viewerName = viewerMap[fileType.category];
        if (viewerName) {
            return await this.loadFileViewer(viewerName);
        }

        // No specific viewer needed (will use DownloadViewer which is in core bundle)
        return true;
    },

    // Lazy load xterm library - Promise-based (optimized!)
    async loadXterm() {
        if (this.xtermLoaded) return true;

        // If already loading, return the existing Promise
        if (this.xtermLoading) {
            return await this.xtermLoading;
        }

        // Create and store Promise for concurrent requests
        this.xtermLoading = (async () => {
            try {
                // Load CSS
                const link = document.createElement('link');
                link.rel = 'stylesheet';
                link.href = '/static/css/xterm.min.css';
                document.head.appendChild(link);

                // Load xterm.js
                await this.loadScript('/static/js/xterm.min.js');
                // Load fit addon
                await this.loadScript('/static/js/xterm-addon-fit.min.js');

                this.xtermLoaded = true;
                return true;
            } catch (err) {
                console.error('Failed to load xterm:', err);
                return false;
            } finally {
                this.xtermLoading = null;
            }
        })();

        return await this.xtermLoading;
    },

    // Helper to load script dynamically
    loadScript(src) {
        return new Promise((resolve, reject) => {
            const script = document.createElement('script');
            script.src = src;
            script.onload = resolve;
            script.onerror = reject;
            document.body.appendChild(script);
        });
    },

    // ========== File Cache Methods (LRU Cache) ==========

    // Get file from cache
    getCachedFile(path) {
        const cached = this.fileCache.get(path);
        if (cached) {
            // Update timestamp for LRU
            cached.timestamp = Date.now();
            this.fileCache.delete(path);
            this.fileCache.set(path, cached);
            console.log(`[FileCache] HIT: ${path} (${this.formatFileSize(cached.size)})`);
            return cached;
        }
        console.log(`[FileCache] MISS: ${path}`);
        return null;
    },

    // Add file to cache with LRU eviction
    setCachedFile(path, fileData) {
        const size = fileData.size || 0;

        // Don't cache files larger than max cache size
        if (size > this.fileCacheMaxSize) {
            console.log(`[FileCache] File too large to cache: ${path} (${this.formatFileSize(size)})`);
            return;
        }

        // Evict old entries if cache would exceed max size
        while (this.fileCacheCurrentSize + size > this.fileCacheMaxSize && this.fileCache.size > 0) {
            // Remove oldest entry (first item in Map)
            const firstKey = this.fileCache.keys().next().value;
            const firstValue = this.fileCache.get(firstKey);
            this.fileCacheCurrentSize -= (firstValue.size || 0);
            this.fileCache.delete(firstKey);
            console.log(`[FileCache] EVICT: ${firstKey} (${this.formatFileSize(firstValue.size || 0)})`);
        }

        // Add to cache
        fileData.timestamp = Date.now();
        this.fileCache.set(path, fileData);
        this.fileCacheCurrentSize += size;

        console.log(`[FileCache] SET: ${path} (${this.formatFileSize(size)}) - Total: ${this.formatFileSize(this.fileCacheCurrentSize)}/${this.formatFileSize(this.fileCacheMaxSize)}`);
    },

    // Clear file cache
    clearFileCache() {
        this.fileCache.clear();
        this.fileCacheCurrentSize = 0;
        console.log('[FileCache] Cleared');
    },

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
            // Close events dropdown when clicking outside
            if (!e.target.closest('.events-bell') && !e.target.closest('.events-dropdown')) {
                if (this.eventsOpen) {
                    this.eventsOpen = false;
                    const dropdown = document.getElementById('events-dropdown');
                    if (dropdown) dropdown.classList.add('hidden');
                }
            }
        });

        // Close modals on backdrop click
        document.addEventListener('click', (e) => {
            // Check if click was on modal backdrop (not on modal-content)
            if (e.target.classList.contains('modal') && !e.target.classList.contains('hidden')) {
                const modalId = e.target.id;

                // Special handling for terminal modal
                if (modalId === 'modal-terminal') {
                    this.closeTerminal();
                } else {
                    this.closeModal(modalId);
                }
            }
        });

        // Close modals on Escape key
        document.addEventListener('keydown', (e) => {
            if (e.key === 'Escape') {
                // Find any open modal
                const openModal = document.querySelector('.modal:not(.hidden)');
                if (openModal) {
                    const modalId = openModal.id;

                    // Special handling for terminal modal
                    if (modalId === 'modal-terminal') {
                        this.closeTerminal();
                    } else {
                        this.closeModal(modalId);
                    }
                }
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
        // Stop events check
        this.stopEventsCheck();
        this.eventsLastId = 0;
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
        if (this.isAdmin()) {
            document.body.classList.add('is-admin');
        } else {
            document.body.classList.remove('is-admin');
        }

        // Load initial page
        this.navigateTo('dashboard');

        // Start checking for new events
        this.startEventsCheck();
    },

    /**
     * Check if current user has admin role
     * @returns {boolean} true if user is admin, false otherwise
     */
    isAdmin() {
        return this.user && this.user.role === UserRole.ADMIN;
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
        let navUpdated = false;
        document.querySelectorAll('.nav-item').forEach(item => {
            const isActive = item.dataset.page === page;
            item.classList.toggle('active', isActive);
            if (isActive) {
                navUpdated = true;
            }
        });
        if (!navUpdated) {
            console.warn('[App] No nav item found for page:', page);
        }

        // Show page
        document.querySelectorAll('.content-page').forEach(p => {
            p.classList.add('hidden');
        });
        const targetPage = document.getElementById('page-' + page);
        if (targetPage) {
            targetPage.classList.remove('hidden');
        } else {
            console.error('[App] Page not found: page-' + page);
        }

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
                this.loadXterm().then(() => this.initHostTerminal());
                break;
            case 'files':
                this.initFileManager();
                break;
            case 'plugins':
                console.log('[App] Loading plugins page');
                break;
        }
    },

    // Cleanup when leaving terminal page
    cleanupHostTerminal() {
        // Clear reconnect timer
        if (this.hostTerminalReconnectTimer) {
            clearTimeout(this.hostTerminalReconnectTimer);
            this.hostTerminalReconnectTimer = null;
        }
        this.hostTerminalReconnecting = false;
        this.hostTerminalReconnectAttempts = 0;

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

    // Get WebSocket CSRF token
    async getWSToken() {
        try {
            const response = await this.authFetch('/api/auth/ws-token');
            if (!response.ok) {
                throw new Error('Failed to get WebSocket token');
            }
            const data = await response.json();
            return data.token;
        } catch (error) {
            console.error('Failed to get WS token:', error);
            return null;
        }
    },

    // Calculate exponential backoff delay
    getReconnectDelay(attempt) {
        // Exponential backoff: 1s, 2s, 4s, 8s, 16s, max 30s
        const baseDelay = 1000;
        const maxDelay = 30000;
        const delay = Math.min(baseDelay * Math.pow(2, attempt), maxDelay);
        return delay;
    },

    // Show reconnection status in terminal
    showReconnectionStatus(terminal, attempt, nextRetryMs) {
        if (!terminal) return;
        const nextRetrySec = Math.round(nextRetryMs / 1000);
        terminal.writeln(`\r\n\x1b[33mConnection lost. Reconnecting... (attempt ${attempt + 1}, next retry in ${nextRetrySec}s)\x1b[0m`);
    },

    // Events methods
    toggleEvents(event) {
        if (event) event.stopPropagation();
        this.eventsOpen = !this.eventsOpen;
        const dropdown = document.getElementById('events-dropdown');
        if (this.eventsOpen) {
            dropdown.classList.remove('hidden');
            this.loadEvents();
        } else {
            dropdown.classList.add('hidden');
        }
    },

    // Start periodic check for new events
    startEventsCheck() {
        // Initial check
        this.checkNewEvents();
        // Check every 30 seconds
        this.eventsCheckInterval = setInterval(() => this.checkNewEvents(), 30000);
    },

    stopEventsCheck() {
        if (this.eventsCheckInterval) {
            clearInterval(this.eventsCheckInterval);
            this.eventsCheckInterval = null;
        }
    },

    async checkNewEvents() {
        if (!this.user) return;

        try {
            const url = this.eventsLastId > 0
                ? `/api/events?since=${this.eventsLastId}`
                : '/api/events?limit=1';

            const response = await this.authFetch(url);
            if (!response.ok) return;

            const data = await response.json();
            const newLastId = data.lastId || 0;
            const events = data.events || [];

            // First check - just save lastId without showing badge
            if (this.eventsLastId === 0) {
                this.eventsLastId = newLastId;
                return;
            }

            // Show badge if there are new events and dropdown is closed
            if (events.length > 0 && !this.eventsOpen) {
                const badge = document.getElementById('events-badge');
                if (badge) {
                    badge.classList.remove('hidden');
                }
            }

            // Update lastId for next check
            if (newLastId > this.eventsLastId) {
                this.eventsLastId = newLastId;
            }
        } catch (error) {
            // Silently fail
        }
    },

    async loadEvents() {
        try {
            const response = await this.authFetch('/api/events?limit=50');
            if (!response.ok) return;

            const data = await response.json();
            const events = data.events || [];
            this.eventsLastId = data.lastId || 0;
            this.renderEvents(events);

            // Hide badge when viewing events
            const badge = document.getElementById('events-badge');
            if (badge) {
                badge.classList.add('hidden');
            }
        } catch (error) {
            console.error('Failed to load events:', error);
        }
    },

    renderEvents(events) {
        const list = document.getElementById('events-list');

        if (!events || events.length === 0) {
            list.innerHTML = '<div class="events-empty">No events yet</div>';
            return;
        }

        const eventLabels = {
            'login': 'Login',
            'login_failed': 'Login Failed',
            'logout': 'Logout',
            'terminal_host': 'Host Terminal',
            'terminal_container': 'Container Terminal',
            'container_start': 'Container Start',
            'container_stop': 'Container Stop',
            'container_restart': 'Container Restart',
            'container_remove': 'Container Remove',
            'container_create': 'Container Create',
            'image_pull': 'Image Pull',
            'image_remove': 'Image Remove',
            'system_reboot': 'System Reboot',
            'system_shutdown': 'System Shutdown'
        };

        list.innerHTML = events.map(event => {
            const time = new Date(event.timestamp).toLocaleString();
            const label = eventLabels[event.type] || event.type;
            const statusIcon = event.success ? '' : ' (failed)';

            return `
                <div class="event-item">
                    <div class="event-row">
                        <span class="event-type ${event.type}">${label}${statusIcon}</span>
                        <span class="event-user">${event.username || 'unknown'}</span>
                        <span class="event-time">${time}</span>
                    </div>
                    <div class="event-row">
                        <span class="event-ip">${event.ip}</span>
                        ${event.details ? `<span class="event-details">${event.details}</span>` : ''}
                    </div>
                </div>
            `;
        }).join('');
    },

    // Add command to history for host terminal (saves to server via WebSocket)
    addToHistoryServer(command, socket) {
        // Don't add empty commands or duplicates of the last command
        if (!command.trim() || command === this.commandHistory[this.commandHistory.length - 1]) {
            return;
        }

        // Add to local array immediately
        this.commandHistory.push(command);

        // Send to server via WebSocket (non-blocking)
        if (socket && socket.readyState === WebSocket.OPEN) {
            try {
                socket.send(JSON.stringify({
                    type: 'save_command',
                    command: command
                }));
            } catch (e) {
                console.warn('Failed to save command via WebSocket:', e);
            }
        }
    },

    // Add command to history for container terminals (saves to localStorage)
    addToHistoryLocal(command) {
        // Don't add empty commands or duplicates of the last command
        if (!command.trim() || command === this.commandHistory[this.commandHistory.length - 1]) {
            return;
        }

        // Add to local array immediately
        this.commandHistory.push(command);

        // Save to localStorage with container-specific key
        if (this.currentContainerId) {
            try {
                const toSave = this.commandHistory.slice(-100); // Keep last 100 commands
                const storageKey = `containerHistory_${this.currentContainerId}`;
                localStorage.setItem(storageKey, JSON.stringify(toSave));
            } catch (e) {
                console.warn('Failed to save to localStorage:', e);
            }
        }
    },

    // Load container terminal history from localStorage
    loadContainerHistory(containerId) {
        try {
            const storageKey = `containerHistory_${containerId}`;
            const saved = localStorage.getItem(storageKey);
            if (saved) {
                this.commandHistory = JSON.parse(saved);
            } else {
                this.commandHistory = [];
            }
        } catch (e) {
            console.warn('Failed to load container history:', e);
            this.commandHistory = [];
        }
    },

    // Replace current line in terminal with a command from history
    replaceTerminalLine(socket, command) {
        if (!socket || socket.readyState !== WebSocket.OPEN) {
            return;
        }

        // Clear entire line using Ctrl+U (works in bash, sh, ash, dash, zsh)
        // This clears from cursor to beginning, which is more reliable than backspaces
        socket.send(JSON.stringify({ type: 'stdin', data: '\x15' }));

        // Type the command from history
        this.currentLine = command;
        socket.send(JSON.stringify({ type: 'stdin', data: command }));
    },

    // Setup terminal input handler (shared between host and container terminals)
    setupTerminalInputHandler(terminal, socket, saveHistoryFn) {
        terminal.onData(data => {
            if (!socket || socket.readyState !== WebSocket.OPEN) {
                return;
            }

            // Check for special keys
            const charCode = data.charCodeAt(0);

            // Arrow Up - show previous command from history
            if (data === '\x1b[A') {
                if (this.commandHistory.length === 0) return;

                // First time pressing up - save current line and start from end
                if (this.historyIndex === -1) {
                    this.savedLine = this.currentLine;
                    this.historyIndex = this.commandHistory.length - 1;
                } else if (this.historyIndex > 0) {
                    this.historyIndex--;
                }

                this.replaceTerminalLine(socket, this.commandHistory[this.historyIndex]);
                return;
            }

            // Arrow Down - show next command from history
            if (data === '\x1b[B') {
                if (this.historyIndex === -1) return;

                this.historyIndex++;

                // Reached end of history - restore saved line
                if (this.historyIndex >= this.commandHistory.length) {
                    this.historyIndex = -1;
                    this.replaceTerminalLine(socket, this.savedLine);
                } else {
                    this.replaceTerminalLine(socket, this.commandHistory[this.historyIndex]);
                }
                return;
            }

            // Arrow Left or Right - allow cursor movement, don't track position
            if (data === '\x1b[D' || data === '\x1b[C') {
                // Just pass through to server, shell will handle cursor position
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Enter - save command to history
            if (data === '\r' || data === '\n') {
                if (this.currentLine.trim()) {
                    saveHistoryFn(this.currentLine.trim());
                }
                this.currentLine = '';
                this.historyIndex = -1;
                this.savedLine = '';
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Backspace or Delete
            if (charCode === 127 || charCode === 8) {
                if (this.currentLine.length > 0) {
                    this.currentLine = this.currentLine.slice(0, -1);
                }
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Ctrl+A - move to beginning
            if (data === '\x01') {
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Ctrl+K - clear to end of line
            if (data === '\x0b') {
                this.currentLine = '';
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Ctrl+U - clear line
            if (data === '\x15') {
                this.currentLine = '';
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Ctrl+C - clear current line buffer
            if (data === '\x03') {
                this.currentLine = '';
                this.historyIndex = -1;
                this.savedLine = '';
                socket.send(JSON.stringify({ type: 'stdin', data: data }));
                return;
            }

            // Regular character - add to current line
            if (charCode >= 32 && charCode < 127) {
                this.currentLine += data;
            }

            // Send to server
            socket.send(JSON.stringify({ type: 'stdin', data: data }));
        });
    },

    // Connect/reconnect host terminal WebSocket
    async connectHostTerminalSocket() {
        // Clear any existing reconnect timer
        if (this.hostTerminalReconnectTimer) {
            clearTimeout(this.hostTerminalReconnectTimer);
            this.hostTerminalReconnectTimer = null;
        }

        // Close existing socket if any
        if (this.hostTerminalSocket) {
            this.hostTerminalSocket.close();
            this.hostTerminalSocket = null;
        }

        if (!this.hostTerminal) {
            console.error('Host terminal not initialized');
            return;
        }

        if (this.hostTerminalReconnecting) {
            this.hostTerminal.writeln(`\r\nReconnecting...\r\n`);
        } else {
            this.hostTerminal.writeln('Connecting to host...\r\n');
        }

        // Get CSRF token for WebSocket
        const wsToken = await this.getWSToken();
        if (!wsToken) {
            this.hostTerminal.writeln('\x1b[31mFailed to authenticate WebSocket connection\x1b[0m');
            // Schedule reconnect if this is a reconnection attempt
            if (this.hostTerminalReconnecting && this.currentPage === 'terminal') {
                this.scheduleHostTerminalReconnect();
            }
            return;
        }

        // Connect WebSocket with CSRF token
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/terminal?ws_token=${encodeURIComponent(wsToken)}`;

        try {
            this.hostTerminalSocket = new WebSocket(wsUrl);

            this.hostTerminalSocket.onopen = () => {
                // Reset reconnection state on successful connection
                this.hostTerminalReconnecting = false;
                this.hostTerminalReconnectAttempts = 0;

                if (this.hostTerminal) this.hostTerminal.writeln('\x1b[32mConnected!\x1b[0m\r\n');

                // Send initial resize
                if (this.hostTerminalFitAddon) {
                    const dims = this.hostTerminalFitAddon.proposeDimensions();
                    if (dims && this.hostTerminalSocket) {
                        this.hostTerminalSocket.send(JSON.stringify({
                            type: 'resize',
                            cols: dims.cols,
                            rows: dims.rows
                        }));
                    }
                }
            };

            this.hostTerminalSocket.onmessage = (event) => {
                // Try to parse as JSON (for history message)
                try {
                    const msg = JSON.parse(event.data);
                    if (msg.type === 'history' && Array.isArray(msg.commands)) {
                        // Load command history from server
                        this.commandHistory = msg.commands;
                        return;
                    }
                } catch (e) {
                    // Not JSON, treat as terminal output
                }

                // Write to terminal
                if (this.hostTerminal) this.hostTerminal.write(event.data);
            };

            this.hostTerminalSocket.onclose = (event) => {
                // Only attempt reconnect if we're still on terminal page and not intentionally closing
                if (this.currentPage === 'terminal' && !event.wasClean && event.code !== 1000) {
                    this.scheduleHostTerminalReconnect();
                } else if (this.hostTerminal) {
                    this.hostTerminal.writeln('\r\n\x1b[31mConnection closed\x1b[0m');
                }
            };

            this.hostTerminalSocket.onerror = (error) => {
                console.error('WebSocket error:', error);
                // onclose will be called after onerror, so reconnection will be handled there
            };

            // Setup terminal input handler with server history support
            this.setupTerminalInputHandler(
                this.hostTerminal,
                this.hostTerminalSocket,
                (cmd) => this.addToHistoryServer(cmd, this.hostTerminalSocket)
            );

        } catch (error) {
            this.hostTerminal.writeln('\r\n\x1b[31mFailed to connect: ' + error.message + '\x1b[0m');
            // Schedule reconnect on error
            if (this.currentPage === 'terminal') {
                this.scheduleHostTerminalReconnect();
            }
        }
    },

    // Schedule host terminal reconnection with exponential backoff
    scheduleHostTerminalReconnect() {
        this.hostTerminalReconnecting = true;
        const delay = this.getReconnectDelay(this.hostTerminalReconnectAttempts);

        this.showReconnectionStatus(this.hostTerminal, this.hostTerminalReconnectAttempts, delay);

        this.hostTerminalReconnectAttempts++;

        this.hostTerminalReconnectTimer = setTimeout(() => {
            if (this.currentPage === 'terminal') {
                this.connectHostTerminalSocket();
            }
        }, delay);
    },

    // Initialize host terminal
    async initHostTerminal() {
        const container = document.getElementById('host-terminal-container');

        // Check if xterm is available
        if (typeof Terminal === 'undefined') {
            container.innerHTML = '<p style="color: var(--danger); padding: 20px;">Failed to load terminal library.</p>';
            return;
        }

        container.innerHTML = '';

        // Reset reconnection state
        this.hostTerminalReconnecting = false;
        this.hostTerminalReconnectAttempts = 0;
        if (this.hostTerminalReconnectTimer) {
            clearTimeout(this.hostTerminalReconnectTimer);
            this.hostTerminalReconnectTimer = null;
        }

        // Reset command history state
        this.commandHistory = [];
        this.historyIndex = -1;
        this.currentLine = '';
        this.savedLine = '';

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

        // Connect to WebSocket
        await this.connectHostTerminalSocket();
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
            // Update host stats (CPU, memory, uptime, disk, temperatures)
            if (data.hostStats) {
                document.getElementById('info-cpu').textContent = data.hostStats.cpuUsage.toFixed(1) + '%';
                document.getElementById('info-uptime').textContent = this.formatUptime(data.hostStats.uptime);

                // Update memory (using MemAvailable for accurate "free" memory)
                if (data.hostStats.memTotal) {
                    const memFree = this.formatBytes(data.hostStats.memFree);
                    const memTotal = this.formatBytes(data.hostStats.memTotal);
                    document.getElementById('info-memory').textContent = `${memFree} free / ${memTotal} total`;
                }

                // Update disks
                const disksList = document.getElementById('disks-list');
                const singleDisk = document.getElementById('info-disk');

                if (data.hostStats.disks && data.hostStats.disks.length > 0) {
                    if (data.hostStats.disks.length === 1) {
                        // Single disk - show inline
                        const d = data.hostStats.disks[0];
                        singleDisk.textContent = `${this.formatBytes(d.used)} / ${this.formatBytes(d.total)}`;
                        singleDisk.parentElement.style.display = '';
                        disksList.style.display = 'none';
                    } else {
                        // Multiple disks - show list
                        singleDisk.parentElement.style.display = 'none';
                        disksList.innerHTML = data.hostStats.disks.map(d => this.renderDiskItem(d)).join('');
                        disksList.style.display = '';
                    }
                } else if (data.hostStats.diskTotal) {
                    // Fallback to old format
                    const used = data.hostStats.diskTotal - data.hostStats.diskFree;
                    singleDisk.textContent = `${this.formatBytes(used)} / ${this.formatBytes(data.hostStats.diskTotal)}`;
                    singleDisk.parentElement.style.display = '';
                    disksList.style.display = 'none';
                }

                // Update temperatures section
                const tempsSection = document.getElementById('temps-section');
                const tempsCpu = document.getElementById('temps-cpu');
                const tempsStorageContainer = document.getElementById('temps-storage-container');

                const hasCpuTemps = data.hostStats.temperatures && data.hostStats.temperatures.length > 0;
                const hasStorageTemps = data.hostStats.storageTemps && data.hostStats.storageTemps.length > 0;

                // Hide entire section if no temperature data at all
                if (!hasCpuTemps && !hasStorageTemps) {
                    tempsSection.style.display = 'none';
                } else {
                    tempsSection.style.display = '';

                    // Update CPU temperatures
                    if (hasCpuTemps) {
                        tempsCpu.innerHTML = data.hostStats.temperatures.map(t => this.renderTempItem(t)).join('');
                    } else {
                        tempsCpu.innerHTML = '<span class="info-value">No sensors found</span>';
                    }

                    // Update Storage temperatures (grouped by device)
                    if (hasStorageTemps) {
                        tempsStorageContainer.innerHTML = data.hostStats.storageTemps.map(device => this.renderStorageDevice(device)).join('');
                        tempsStorageContainer.style.display = '';
                    } else {
                        tempsStorageContainer.style.display = 'none';
                        tempsStorageContainer.innerHTML = '';
                    }
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
        const isAdmin = this.isAdmin();
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
        this.logsContainerId = id;
        this.stopAutoLogs();
        this.showModal('modal-logs');
        await this.fetchLogs();
    },

    async fetchLogs() {
        if (!this.logsContainerId) return;

        const logsContent = document.getElementById('logs-content');
        const wasScrolledToBottom = logsContent.scrollHeight - logsContent.scrollTop <= logsContent.clientHeight + 50;

        // Only show loading on first load
        if (!logsContent.querySelector('.log-line')) {
            logsContent.innerHTML = '<div class="log-loading">Loading...</div>';
        }

        try {
            const response = await this.authFetch(`/api/containers/${this.logsContainerId}/logs?tail=200`);
            if (!response.ok) throw new Error('Failed to load logs');
            const data = await response.json();

            if (!data.lines || data.lines.length === 0) {
                logsContent.innerHTML = '<div class="log-empty">No logs available</div>';
                return;
            }

            // Build log lines with line numbers
            const html = data.lines.map((line, index) => {
                const lineNum = data.lines.length - index;
                const escapedLine = this.escapeHtml(line) || '&nbsp;';
                return `<div class="log-line"><span class="log-num">${lineNum}</span><span class="log-text">${escapedLine}</span></div>`;
            }).join('');

            logsContent.innerHTML = html;

            // Keep scroll at bottom if it was there
            if (wasScrolledToBottom) {
                logsContent.scrollTop = logsContent.scrollHeight;
            }
        } catch (error) {
            if (error.message !== 'Session expired') {
                logsContent.innerHTML = '<div class="log-error">Error loading logs</div>';
            }
        }
    },

    refreshLogs() {
        this.fetchLogs();
    },

    toggleAutoLogs() {
        const checkbox = document.getElementById('logs-auto-checkbox');
        if (checkbox.checked) {
            this.logsAutoInterval = setInterval(() => this.fetchLogs(), 3000);
        } else {
            this.stopAutoLogs();
        }
    },

    stopAutoLogs() {
        if (this.logsAutoInterval) {
            clearInterval(this.logsAutoInterval);
            this.logsAutoInterval = null;
        }
        const checkbox = document.getElementById('logs-auto-checkbox');
        if (checkbox) checkbox.checked = false;
    },

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
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
                ${this.isAdmin()
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

    // Connect/reconnect container terminal WebSocket
    async connectContainerTerminalSocket() {
        // Clear any existing reconnect timer
        if (this.containerTerminalReconnectTimer) {
            clearTimeout(this.containerTerminalReconnectTimer);
            this.containerTerminalReconnectTimer = null;
        }

        // Close existing socket if any
        if (this.terminalSocket) {
            this.terminalSocket.close();
            this.terminalSocket = null;
        }

        if (!this.terminal || !this.currentContainerId) {
            console.error('Container terminal not initialized or no container ID');
            return;
        }

        if (this.containerTerminalReconnecting) {
            this.terminal.writeln(`\r\nReconnecting...\r\n`);
        } else {
            this.terminal.writeln('Connecting to container...');
        }

        // Get CSRF token for WebSocket
        const wsToken = await this.getWSToken();
        if (!wsToken) {
            this.terminal.writeln('\r\n\x1b[31mFailed to authenticate WebSocket connection\x1b[0m');
            // Schedule reconnect if this is a reconnection attempt
            if (this.containerTerminalReconnecting) {
                this.scheduleContainerTerminalReconnect();
            }
            return;
        }

        // Connect WebSocket with CSRF token
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/containers/${this.currentContainerId}/terminal?ws_token=${encodeURIComponent(wsToken)}`;

        try {
            this.terminalSocket = new WebSocket(wsUrl);

            this.terminalSocket.onopen = () => {
                // Reset reconnection state on successful connection
                this.containerTerminalReconnecting = false;
                this.containerTerminalReconnectAttempts = 0;

                if (this.terminal) this.terminal.writeln('Connected!\r\n');
            };

            this.terminalSocket.onmessage = (event) => {
                // Write to terminal
                if (this.terminal) this.terminal.write(event.data);
            };

            this.terminalSocket.onclose = (event) => {
                // Only attempt reconnect if modal is still open and not intentionally closing
                const modal = document.getElementById('modal-terminal');
                const isModalOpen = modal && !modal.classList.contains('hidden');

                if (isModalOpen && !event.wasClean && event.code !== 1000) {
                    this.scheduleContainerTerminalReconnect();
                } else if (this.terminal) {
                    this.terminal.writeln('\r\n\x1b[31mConnection closed\x1b[0m');
                }
            };

            this.terminalSocket.onerror = (error) => {
                console.error('Container WebSocket error:', error);
                // onclose will be called after onerror, so reconnection will be handled there
            };

            // Setup terminal input handler with localStorage history support
            this.setupTerminalInputHandler(
                this.terminal,
                this.terminalSocket,
                (cmd) => this.addToHistoryLocal(cmd)
            );

        } catch (error) {
            this.terminal.writeln('\r\n\x1b[31mFailed to connect: ' + error.message + '\x1b[0m');
            // Schedule reconnect on error
            const modal = document.getElementById('modal-terminal');
            const isModalOpen = modal && !modal.classList.contains('hidden');
            if (isModalOpen) {
                this.scheduleContainerTerminalReconnect();
            }
        }
    },

    // Schedule container terminal reconnection with exponential backoff
    scheduleContainerTerminalReconnect() {
        this.containerTerminalReconnecting = true;
        const delay = this.getReconnectDelay(this.containerTerminalReconnectAttempts);

        this.showReconnectionStatus(this.terminal, this.containerTerminalReconnectAttempts, delay);

        this.containerTerminalReconnectAttempts++;

        this.containerTerminalReconnectTimer = setTimeout(() => {
            const modal = document.getElementById('modal-terminal');
            const isModalOpen = modal && !modal.classList.contains('hidden');
            if (isModalOpen) {
                this.connectContainerTerminalSocket();
            }
        }, delay);
    },

    // Open terminal
    async openTerminal(containerId) {
        this.showModal('modal-terminal');
        const container = document.getElementById('terminal-container');
        container.innerHTML = '<p style="color: var(--text-secondary); padding: 20px;">Loading terminal...</p>';

        // Load xterm if not loaded
        const loaded = await this.loadXterm();
        if (!loaded || typeof Terminal === 'undefined') {
            container.innerHTML = '<p style="color: var(--danger); padding: 20px;">Failed to load terminal library.</p>';
            return;
        }

        container.innerHTML = '';

        // Save current container ID for history management
        this.currentContainerId = containerId;

        // Reset reconnection state
        this.containerTerminalReconnecting = false;
        this.containerTerminalReconnectAttempts = 0;
        if (this.containerTerminalReconnectTimer) {
            clearTimeout(this.containerTerminalReconnectTimer);
            this.containerTerminalReconnectTimer = null;
        }

        // Load command history from localStorage for this container
        this.loadContainerHistory(containerId);
        this.historyIndex = -1;
        this.currentLine = '';
        this.savedLine = '';

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

        // Connect to WebSocket
        await this.connectContainerTerminalSocket();
    },

    // Close terminal
    closeTerminal() {
        // Clear reconnect timer
        if (this.containerTerminalReconnectTimer) {
            clearTimeout(this.containerTerminalReconnectTimer);
            this.containerTerminalReconnectTimer = null;
        }
        this.containerTerminalReconnecting = false;
        this.containerTerminalReconnectAttempts = 0;

        if (this.terminalSocket) {
            this.terminalSocket.close();
            this.terminalSocket = null;
        }
        if (this.terminal) {
            this.terminal.dispose();
            this.terminal = null;
        }
        this.currentContainerId = null;
        this.terminalFitAddon = null;
        this.closeModal('modal-terminal');
    },

    // Modal helpers
    showModal(id) {
        document.getElementById(id).classList.remove('hidden');
    },

    closeModal(id) {
        document.getElementById(id).classList.add('hidden');
        // Stop auto-refresh when closing logs modal
        if (id === 'modal-logs') {
            this.stopAutoLogs();
            this.logsContainerId = null;
        }
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
    },

    renderTempItem(t) {
        // Temperature thresholds for Orange Pi RV2 (SpacemiT K1)
        // < 50°C = cool, 50-65°C = normal, 65-75°C = warm, 75-85°C = hot, > 85°C = critical
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
    },

    renderStorageDevice(device) {
        const sensorsHtml = device.sensors.map(t => this.renderTempItem(t)).join('');
        return `
            <h3 style="margin-top: 16px;">${device.device}</h3>
            <div class="temps-grid">
                ${sensorsHtml}
            </div>
        `;
    },

    renderDiskItem(disk) {
        const usedPercent = disk.total > 0 ? ((disk.used / disk.total) * 100).toFixed(1) : 0;
        let progressClass = 'normal';
        if (usedPercent > 90) progressClass = 'critical';
        else if (usedPercent > 75) progressClass = 'warning';

        return `
            <div class="disk-item">
                <div class="disk-header">
                    <span class="disk-device">${disk.device}</span>
                    <span class="disk-mount">${disk.mountPoint}</span>
                </div>
                <div class="disk-bar">
                    <div class="disk-bar-fill ${progressClass}" style="width: ${usedPercent}%"></div>
                </div>
                <div class="disk-info">
                    <span>${this.formatBytes(disk.used)} / ${this.formatBytes(disk.total)}</span>
                    <span>${usedPercent}%</span>
                </div>
            </div>
        `;
    },

    // Update system
    updateInfo: null,
    updatePollingInterval: null,

    // Check for updates
    async checkForUpdates() {
        try {
            const response = await this.authFetch('/api/system/update/check');
            if (!response.ok) throw new Error('Failed to check for updates');

            this.updateInfo = await response.json();
            this.renderUpdateButton();
            return this.updateInfo;
        } catch (error) {
            console.error('Failed to check for updates:', error);
            return null;
        }
    },

    // Render update button based on state
    renderUpdateButton() {
        const btn = document.getElementById('system-update-btn');
        if (!btn) return;

        if (!this.updateInfo) {
            btn.textContent = 'Check Updates';
            btn.disabled = false;
            btn.title = '';
            btn.classList.remove('has-update');
            return;
        }

        if (this.updateInfo.isDev) {
            btn.textContent = 'Update (dev)';
            btn.disabled = true;
            btn.title = 'Dev version does not support auto-updates';
            btn.classList.remove('has-update');
            return;
        }

        if (this.updateInfo.updateAvailable) {
            btn.textContent = `Update to ${this.updateInfo.latestVersion}`;
            btn.disabled = false;
            btn.title = `New version available: ${this.updateInfo.latestVersion}`;
            btn.classList.add('has-update');
        } else {
            btn.textContent = 'Up to date';
            btn.disabled = true;
            btn.title = `Current version: ${this.updateInfo.currentVersion}`;
            btn.classList.remove('has-update');
        }
    },

    // Show update modal
    async showUpdateModal() {
        if (!this.updateInfo) {
            await this.checkForUpdates();
        }

        if (!this.updateInfo) {
            this.showToast('Failed to check for updates', 'error');
            return;
        }

        if (this.updateInfo.isDev) {
            this.showToast('Dev version does not support auto-updates', 'info');
            return;
        }

        if (!this.updateInfo.updateAvailable) {
            this.showToast('Already running the latest version', 'info');
            return;
        }

        // Populate modal
        document.getElementById('update-current-version').textContent = this.updateInfo.currentVersion;
        document.getElementById('update-latest-version').textContent = this.updateInfo.latestVersion;
        document.getElementById('update-download-size').textContent = this.formatBytes(this.updateInfo.downloadSize);
        document.getElementById('update-arch').textContent = this.updateInfo.currentArch;

        const releaseNotes = document.getElementById('update-release-notes');
        if (this.updateInfo.releaseNotes) {
            releaseNotes.textContent = this.updateInfo.releaseNotes;
            releaseNotes.parentElement.style.display = '';
        } else {
            releaseNotes.parentElement.style.display = 'none';
        }

        // Reset progress
        document.getElementById('update-progress-container').classList.add('hidden');
        document.getElementById('update-progress-bar').style.width = '0%';
        document.getElementById('update-progress-text').textContent = '';

        // Enable start button
        const startBtn = document.getElementById('update-start-btn');
        startBtn.disabled = false;
        startBtn.textContent = 'Start Update';

        this.showModal('modal-update');
    },

    // Start update process
    async startUpdate() {
        const startBtn = document.getElementById('update-start-btn');
        startBtn.disabled = true;
        startBtn.textContent = 'Updating...';

        // Show progress
        document.getElementById('update-progress-container').classList.remove('hidden');
        this.setUpdateProgress(0, 'Starting update...');

        try {
            const response = await this.authFetch('/api/system/update', { method: 'POST' });
            if (!response.ok) {
                const data = await response.json();
                throw new Error(data.error || 'Failed to start update');
            }

            // Start polling for progress
            this.startUpdatePolling();
        } catch (error) {
            this.showToast(error.message, 'error');
            startBtn.disabled = false;
            startBtn.textContent = 'Retry Update';
            this.setUpdateProgress(0, 'Update failed: ' + error.message);
        }
    },

    // Poll for update status
    startUpdatePolling() {
        if (this.updatePollingInterval) {
            clearInterval(this.updatePollingInterval);
        }

        this.updatePollingInterval = setInterval(async () => {
            try {
                const response = await this.authFetch('/api/system/update/status');
                if (!response.ok) return;

                const data = await response.json();

                if (!data.updating) {
                    clearInterval(this.updatePollingInterval);
                    this.updatePollingInterval = null;

                    if (data.progress && data.progress.stage === 'failed') {
                        this.setUpdateProgress(0, 'Update failed: ' + data.progress.message);
                        const startBtn = document.getElementById('update-start-btn');
                        startBtn.disabled = false;
                        startBtn.textContent = 'Retry Update';
                    }
                    return;
                }

                if (data.progress) {
                    this.setUpdateProgress(data.progress.percent, data.progress.message || data.progress.stage);

                    // If restarting, show message and stop polling
                    if (data.progress.stage === 'restarting') {
                        clearInterval(this.updatePollingInterval);
                        this.updatePollingInterval = null;
                        this.setUpdateProgress(100, 'Restarting service... Page will reload.');

                        // Wait a bit then reload
                        setTimeout(() => {
                            window.location.reload();
                        }, 5000);
                    }
                }
            } catch (error) {
                // Server might be restarting
                if (error.message === 'Session expired' || error.name === 'TypeError') {
                    clearInterval(this.updatePollingInterval);
                    this.updatePollingInterval = null;
                    this.setUpdateProgress(100, 'Service restarting... Page will reload.');
                    setTimeout(() => {
                        window.location.reload();
                    }, 3000);
                }
            }
        }, 1000);
    },

    // Set update progress
    setUpdateProgress(percent, message) {
        document.getElementById('update-progress-bar').style.width = percent + '%';
        document.getElementById('update-progress-text').textContent = message;
    },

    // ========== File Manager ==========

    // Initialize file manager
    async initFileManager() {
        // NOTE: Core modules are NOT loaded here - they're only needed when opening files
        // This saves ~22KB and 1 HTTP request when just browsing

        // Setup event listeners
        document.getElementById('fm-upload-btn').onclick = () => {
            document.getElementById('fm-file-input').click();
        };

        document.getElementById('fm-file-input').onchange = (e) => {
            if (e.target.files.length > 0) {
                this.uploadFiles(e.target.files);
                e.target.value = ''; // Reset input
            }
        };

        document.getElementById('fm-new-folder-btn').onclick = () => {
            this.showNewFolderDialog();
        };

        document.getElementById('fm-new-file-btn').onclick = () => {
            this.showNewFileDialog();
        };

        // Setup drag & drop
        this.setupFileManagerDragDrop();

        // Load root directory
        await this.loadFiles('/');
    },

    // Load files for a given path (with pagination support)
    async loadFiles(path, offset = 0) {
        try {
            const limit = this.fileManagerLimit;
            const url = `/api/files/browse?path=${encodeURIComponent(path)}&offset=${offset}&limit=${limit}`;
            const response = await this.authFetch(url);
            if (!response.ok) {
                throw new Error('Failed to load files');
            }

            const data = await response.json();
            this.fileManagerCurrentPath = data.path;
            this.fileManagerParent = data.parent;
            this.fileManagerFiles = data.items || [];
            this.fileManagerOffset = data.offset || 0;
            this.fileManagerTotalCount = data.total_count || 0;
            this.fileManagerHasMore = data.has_more || false;

            this.renderBreadcrumb(data.path);
            this.renderFiles(data.items || []);
            this.renderPagination(data);
        } catch (error) {
            console.error('Failed to load files:', error);
            this.showToast('Failed to load files', 'error');
        }
    },

    // Render pagination controls
    renderPagination(data) {
        const container = document.getElementById('fm-pagination');
        if (!container) return;

        const { offset, limit, total_count, has_more } = data;
        const currentPage = Math.floor(offset / limit) + 1;
        const totalPages = Math.ceil(total_count / limit);

        // Hide pagination if only one page
        if (totalPages <= 1) {
            container.innerHTML = '';
            container.style.display = 'none';
            return;
        }

        container.style.display = 'flex';

        // Build pagination HTML
        const showingStart = offset + 1;
        const showingEnd = Math.min(offset + limit, total_count);

        container.innerHTML = `
            <div class="pagination-info">
                Showing ${showingStart}-${showingEnd} of ${total_count} items
            </div>
            <div class="pagination-controls">
                <button class="btn btn-sm pagination-btn" ${offset === 0 ? 'disabled' : ''}
                    onclick="App.loadFiles('${this.fileManagerCurrentPath}', ${Math.max(0, offset - limit)})">
                    ‹ Previous
                </button>
                <span class="pagination-pages">
                    Page ${currentPage} of ${totalPages}
                </span>
                <button class="btn btn-sm pagination-btn" ${!has_more ? 'disabled' : ''}
                    onclick="App.loadFiles('${this.fileManagerCurrentPath}', ${offset + limit})">
                    Next ›
                </button>
            </div>
        `;
    },

    // Render breadcrumb navigation
    renderBreadcrumb(path) {
        const breadcrumbPath = document.querySelector('#fm-breadcrumb .breadcrumb-path');
        breadcrumbPath.innerHTML = `
            <svg class="breadcrumb-icon" viewBox="0 0 24 24" fill="currentColor">
                <path d="M10 20v-6h4v6h5v-8h3L12 3 2 12h3v8z"/>
            </svg>
        `;

        const parts = path.split('/').filter(p => p);

        // Home
        const homeSpan = document.createElement('span');
        homeSpan.className = 'breadcrumb-item';
        homeSpan.textContent = 'Home';
        homeSpan.dataset.path = '/';
        homeSpan.onclick = () => this.loadFiles('/');
        breadcrumbPath.appendChild(homeSpan);

        // Path parts
        let currentPath = '';
        parts.forEach((part, index) => {
            currentPath += '/' + part;
            const separator = document.createElement('span');
            separator.className = 'breadcrumb-separator';
            separator.textContent = '/';
            breadcrumbPath.appendChild(separator);

            const span = document.createElement('span');
            span.className = 'breadcrumb-item';
            span.textContent = part;
            span.dataset.path = currentPath;
            const pathToLoad = currentPath; // Capture for closure
            span.onclick = () => this.loadFiles(pathToLoad);
            breadcrumbPath.appendChild(span);
        });
    },

    // Render file list
    renderFiles(files) {
        const tbody = document.getElementById('fm-file-list');

        if (!files || files.length === 0) {
            tbody.innerHTML = `
                <tr class="fm-empty">
                    <td colspan="4">No files or directories</td>
                </tr>
            `;
            return;
        }

        // Sort: directories first, then files, alphabetically
        const sorted = [...files].sort((a, b) => {
            if (a.is_dir && !b.is_dir) return -1;
            if (!a.is_dir && b.is_dir) return 1;
            return a.name.localeCompare(b.name);
        });

        tbody.innerHTML = sorted.map(file => {
            const icon = this.getFileIcon(file);
            const size = file.is_dir ? '-' : this.formatFileSize(file.size);
            const date = new Date(file.mod_time).toLocaleString();

            return `
                <tr class="fm-row ${file.is_dir ? 'fm-dir' : 'fm-file'}" data-path="${file.path}" data-name="${file.name}" data-is-dir="${file.is_dir}">
                    <td class="fm-col-name">
                        <div class="fm-name-cell">
                            ${icon}
                            <span class="fm-name">${this.escapeHtml(file.name)}</span>
                        </div>
                    </td>
                    <td class="fm-col-size">${size}</td>
                    <td class="fm-col-modified">${date}</td>
                    <td class="fm-col-actions">
                        ${this.getFileActions(file)}
                    </td>
                </tr>
            `;
        }).join('');

        // Add click handlers for navigation and file opening
        tbody.querySelectorAll('.fm-row').forEach(row => {
            const isDir = row.dataset.isDir === 'true';
            const nameCell = row.querySelector('.fm-name-cell');
            nameCell.style.cursor = 'pointer';

            if (isDir) {
                nameCell.onclick = () => this.loadFiles(row.dataset.path);
            } else {
                nameCell.onclick = () => this.openFileEditor(row.dataset.path, row.dataset.name);
            }
        });
    },

    // Get icon for file type
    getFileIcon(file) {
        if (file.is_dir) {
            return `<svg class="fm-icon fm-icon-folder" viewBox="0 0 24 24" fill="currentColor">
                <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z"/>
            </svg>`;
        }

        const ext = file.name.split('.').pop().toLowerCase();

        // Image files
        if (['jpg', 'jpeg', 'png', 'gif', 'svg', 'webp'].includes(ext)) {
            return `<svg class="fm-icon fm-icon-image" viewBox="0 0 24 24" fill="currentColor">
                <rect x="3" y="3" width="18" height="18" rx="2"/>
                <circle cx="8.5" cy="8.5" r="1.5"/>
                <path d="M21 15l-5-5L5 21"/>
            </svg>`;
        }

        // Code files
        if (['js', 'ts', 'go', 'py', 'java', 'c', 'cpp', 'rs', 'html', 'css'].includes(ext)) {
            return `<svg class="fm-icon fm-icon-code" viewBox="0 0 24 24" fill="currentColor">
                <polyline points="16 18 22 12 16 6"/>
                <polyline points="8 6 2 12 8 18"/>
            </svg>`;
        }

        // Archive files
        if (['zip', 'tar', 'gz', 'rar', '7z'].includes(ext)) {
            return `<svg class="fm-icon fm-icon-archive" viewBox="0 0 24 24" fill="currentColor">
                <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
                <path d="M14 2v6h6M12 11v2M12 15v2"/>
            </svg>`;
        }

        // Default file icon
        return `<svg class="fm-icon fm-icon-file" viewBox="0 0 24 24" fill="currentColor">
            <path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/>
            <polyline points="13 2 13 9 20 9"/>
        </svg>`;
    },

    // Format file size
    formatFileSize(bytes) {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return Math.round(bytes / Math.pow(k, i) * 100) / 100 + ' ' + sizes[i];
    },

    // Get file actions dropdown
    getFileActions(file) {
        const path = this.escapeHtml(file.path);
        const name = this.escapeHtml(file.name);
        const isDir = file.is_dir;

        let menuItems = '';

        if (!isDir) {
            menuItems += `<button class="dropdown-item" onclick="App.downloadFile('${path}', '${name}')">Download</button>`;
        }

        menuItems += `<button class="dropdown-item" onclick="App.showRenameDialog('${path}', '${name}')">Rename</button>`;
        menuItems += `<div class="dropdown-divider"></div>`;
        menuItems += `<button class="dropdown-item btn-danger" onclick="App.confirmDeleteFile('${path}', '${name}', ${isDir})">Delete</button>`;

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

    // Upload files
    async uploadFiles(files) {
        const formData = new FormData();
        formData.append('path', this.fileManagerCurrentPath);

        for (let i = 0; i < files.length; i++) {
            formData.append('files', files[i]);
        }

        try {
            this.showToast(`Uploading ${files.length} file(s)...`, 'info');

            const response = await this.authFetch('/api/files/upload', {
                method: 'POST',
                body: formData
            });

            if (!response.ok) {
                throw new Error('Upload failed');
            }

            const result = await response.json();
            this.showToast(`Uploaded ${result.count} file(s)`, 'success');

            // Reload current directory
            await this.loadFiles(this.fileManagerCurrentPath);
        } catch (error) {
            console.error('Upload error:', error);
            this.showToast('Upload failed', 'error');
        }
    },

    // Download file
    downloadFile(path, name) {
        window.location.href = `/api/files/download?path=${encodeURIComponent(path)}`;
    },

    // Confirm delete
    confirmDeleteFile(path, name, isDir) {
        const type = isDir ? 'directory' : 'file';
        if (confirm(`Are you sure you want to delete ${type} "${name}"?${isDir ? ' This will delete all contents.' : ''}`)) {
            this.deleteFile(path, name, isDir);
        }
    },

    // Delete file or directory
    async deleteFile(path, name, isDir) {
        try {
            const response = await this.authFetch(`/api/files?path=${encodeURIComponent(path)}`, {
                method: 'DELETE'
            });

            if (!response.ok) {
                throw new Error('Delete failed');
            }

            const type = isDir ? 'Directory' : 'File';
            this.showToast(`${type} deleted`, 'success');

            // Reload current directory
            await this.loadFiles(this.fileManagerCurrentPath);
        } catch (error) {
            console.error('Delete error:', error);
            this.showToast('Delete failed', 'error');
        }
    },

    // Show new folder dialog
    showNewFolderDialog() {
        const name = prompt('Enter folder name:');
        if (name && name.trim()) {
            this.createFolder(name.trim());
        }
    },

    // Create new folder
    async createFolder(name) {
        try {
            const response = await this.authFetch('/api/files/mkdir', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    path: this.fileManagerCurrentPath,
                    name: name
                })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error || 'Failed to create folder');
            }

            this.showToast('Folder created', 'success');

            // Reload current directory
            await this.loadFiles(this.fileManagerCurrentPath);
        } catch (error) {
            console.error('Create folder error:', error);
            this.showToast(error.message || 'Failed to create folder', 'error');
        }
    },

    // Show new file dialog
    showNewFileDialog() {
        const name = prompt('Enter file name (e.g., document.txt):');
        if (name && name.trim()) {
            this.createFile(name.trim());
        }
    },

    // Create new file
    async createFile(name) {
        try {
            const response = await this.authFetch('/api/files/create', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    path: this.fileManagerCurrentPath,
                    name: name
                })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error || 'Failed to create file');
            }

            this.showToast('File created', 'success');

            // Reload current directory
            await this.loadFiles(this.fileManagerCurrentPath);
        } catch (error) {
            console.error('Create file error:', error);
            this.showToast(error.message || 'Failed to create file', 'error');
        }
    },

    // Show rename dialog
    showRenameDialog(path, oldName) {
        const newName = prompt('Enter new name:', oldName);
        if (newName && newName.trim() && newName !== oldName) {
            this.renameFile(path, newName.trim());
        }
    },

    // Rename file or directory
    async renameFile(oldPath, newName) {
        try {
            const response = await this.authFetch('/api/files/rename', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    old_path: oldPath,
                    new_name: newName
                })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error || 'Failed to rename');
            }

            this.showToast('Renamed successfully', 'success');

            // Reload current directory
            await this.loadFiles(this.fileManagerCurrentPath);
        } catch (error) {
            console.error('Rename error:', error);
            this.showToast(error.message || 'Rename failed', 'error');
        }
    },

    // Setup drag & drop for file upload
    setupFileManagerDragDrop() {
        const dropZone = document.getElementById('fm-drop-zone');
        const overlay = document.getElementById('fm-drop-overlay');

        let dragCounter = 0;

        dropZone.addEventListener('dragenter', (e) => {
            e.preventDefault();
            dragCounter++;
            overlay.classList.remove('hidden');
        });

        dropZone.addEventListener('dragleave', (e) => {
            e.preventDefault();
            dragCounter--;
            if (dragCounter === 0) {
                overlay.classList.add('hidden');
            }
        });

        dropZone.addEventListener('dragover', (e) => {
            e.preventDefault();
        });

        dropZone.addEventListener('drop', (e) => {
            e.preventDefault();
            dragCounter = 0;
            overlay.classList.add('hidden');

            const files = e.dataTransfer.files;
            if (files.length > 0) {
                this.uploadFiles(files);
            }
        });
    },

    // HTML escape helper
    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    },

    // ========== File Editor ==========

    currentFileViewer: null,
    currentFileViewerPath: null,
    boundFileViewerSave: null,
    boundToolbarUpdate: null,

    // Open file with appropriate viewer (with cache support!)
    async openFileViewer(path, name) {
        try {
            this.showToast('Loading file...', 'info');

            // Check cache first
            let fileData = this.getCachedFile(path);

            if (!fileData) {
                // Cache miss - fetch from server
                const response = await this.authFetch(`/api/files/read?path=${encodeURIComponent(path)}`);
                if (!response.ok) {
                    const error = await response.text();
                    throw new Error(error || 'Failed to load file');
                }

                fileData = await response.json();
                fileData.path = path;

                // Handle streaming for large files
                if (fileData.streamingRequired && fileData.encoding === 'stream') {
                    // For large binary files, use streaming endpoint
                    fileData.streamUrl = `/api/files/stream?path=${encodeURIComponent(path)}`;
                    console.log('[FileViewer] Large file detected, using streaming mode');
                }

                // Cache file data (only if not streaming)
                if (!fileData.streamingRequired) {
                    this.setCachedFile(path, fileData);
                }
            } else {
                console.log('[FileViewer] Using cached file data');
            }

            fileData.path = path;

            // Load core bundle FIRST (contains FileTypeDetector)
            const coreLoaded = await this.loadFileManagerCore();
            if (!coreLoaded) {
                throw new Error('Failed to load File Manager core');
            }

            // NOW we can detect file type and load appropriate viewer
            const fileType = FileTypeDetector.detect(fileData.name, fileData.mimeType);
            const viewerLoaded = await this.loadViewerForFileType(fileType);

            if (!viewerLoaded) {
                throw new Error('Failed to load file viewer');
            }

            // Destroy previous viewer if exists
            if (this.currentFileViewer) {
                this.currentFileViewer.destroy();
                this.currentFileViewer = null;
            }

            // Create appropriate viewer
            this.currentFileViewer = FileViewerFactory.createViewer(fileData);
            this.currentFileViewerPath = path;

            // Update modal UI (use fileType from viewer for consistency)
            const viewerFileType = this.currentFileViewer.fileType;
            document.getElementById('file-viewer-title').textContent =
                this.currentFileViewer.canEdit() ? 'Edit File' : 'View File';
            document.getElementById('file-viewer-name').textContent = fileData.name;
            document.getElementById('file-viewer-type').textContent = FileTypeDetector.getTypeName(viewerFileType.category);
            document.getElementById('file-viewer-size').textContent = this.formatFileSize(fileData.size);

            // Render viewer content
            const contentContainer = document.getElementById('file-viewer-content');
            await this.currentFileViewer.render(contentContainer);

            // Setup toolbar actions
            this.setupFileViewerActions();

            // Listen for save events from viewer
            this.boundFileViewerSave = this.handleFileViewerSave.bind(this);
            this.boundToolbarUpdate = this.setupFileViewerActions.bind(this);

            window.addEventListener('file-viewer-save', this.boundFileViewerSave);
            window.addEventListener('file-viewer-toolbar-update', this.boundToolbarUpdate);

            // Show modal
            this.showModal('modal-file-viewer');

            this.showToast('File loaded', 'success');

        } catch (error) {
            console.error('Failed to load file:', error);
            this.showToast(error.message || 'Failed to load file', 'error');
        }
    },

    // Setup viewer toolbar actions
    setupFileViewerActions() {
        const actionsContainer = document.getElementById('file-viewer-actions');
        actionsContainer.innerHTML = '';

        if (!this.currentFileViewer) return;

        const actions = this.currentFileViewer.getToolbarActions();

        actions.forEach(action => {
            if (action.isInfo) {
                const span = document.createElement('span');
                span.className = 'file-viewer-action-info';
                span.textContent = action.label;
                span.style.color = 'var(--text-secondary)';
                span.style.fontSize = '0.85em';
                actionsContainer.appendChild(span);
            } else if (action.action) {
                const btn = document.createElement('button');
                btn.className = action.primary ? 'btn btn-primary' : 'btn';

                // Add icon if present
                if (action.icon) {
                    // Check if icon is HTML (SVG) or text (emoji)
                    if (action.icon.startsWith('<')) {
                        // SVG icon - parse as HTML
                        const iconSpan = document.createElement('span');
                        iconSpan.innerHTML = action.icon;
                        iconSpan.style.display = 'inline-flex';
                        iconSpan.style.alignItems = 'center';
                        btn.appendChild(iconSpan);
                    } else {
                        // Text icon (emoji) - use as text
                        btn.appendChild(document.createTextNode(action.icon + ' '));
                    }
                }

                // Add label text
                const labelSpan = document.createElement('span');
                labelSpan.textContent = action.label;
                btn.appendChild(labelSpan);

                btn.disabled = action.disabled;
                btn.onclick = action.action;
                actionsContainer.appendChild(btn);
            }
        });
    },

    // Handle save event from viewer
    async handleFileViewerSave(event) {
        const { path, content } = event.detail;
        await this.saveFileContent(path, content);
    },

    // Close file viewer
    closeFileViewer() {
        if (this.currentFileViewer) {
            if (this.currentFileViewer.canEdit() && this.currentFileViewer.hasChanges) {
                if (!confirm('You have unsaved changes. Are you sure you want to close?')) {
                    return;
                }
            }

            this.currentFileViewer.destroy();
            this.currentFileViewer = null;
        }

        // Remove event listeners
        if (this.boundFileViewerSave) {
            window.removeEventListener('file-viewer-save', this.boundFileViewerSave);
            this.boundFileViewerSave = null;
        }
        if (this.boundToolbarUpdate) {
            window.removeEventListener('file-viewer-toolbar-update', this.boundToolbarUpdate);
            this.boundToolbarUpdate = null;
        }

        this.currentFileViewerPath = null;
        this.closeModal('modal-file-viewer');
    },

    // Save file content
    async saveFileContent(path, content) {
        try {
            const response = await this.authFetch('/api/files/write', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path, content })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error || 'Failed to save file');
            }

            const result = await response.json();

            if (this.currentFileViewer && this.currentFileViewer.canEdit()) {
                this.currentFileViewer.setContent(content);
            }

            document.getElementById('file-viewer-size').textContent = this.formatFileSize(result.size);
            this.showToast('File saved successfully', 'success');
            this.setupFileViewerActions();

            return result;
        } catch (error) {
            console.error('Failed to save file:', error);
            this.showToast(error.message || 'Failed to save file', 'error');
            throw error;
        }
    },

    // Backward compatibility - redirect old method to new one
    async openFileEditor(path, name) {
        return this.openFileViewer(path, name);
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
