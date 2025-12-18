// ============================================================================
// IconHelper - SVG icon generation
// ============================================================================

class IconHelper {
    static ICON_PATHS = {
        'save': '<path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"/><polyline points="17 21 17 13 7 13 7 21"/><polyline points="7 3 7 8 15 8"/>',
        'download': '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/>',
        'upload': '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/>',
        'external-link': '<path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/>',
        'zoom-in': '<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="11" y1="8" x2="11" y2="14"/><line x1="8" y1="11" x2="14" y2="11"/>',
        'zoom-out': '<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="8" y1="11" x2="14" y2="11"/>',
        'rotate-cw': '<polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>',
        'rotate-ccw': '<polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>',
        'refresh': '<polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>',
        'play': '<polygon points="5 3 19 12 5 21 5 3"/>',
        'pause': '<rect x="6" y="4" width="4" height="16"/><rect x="14" y="4" width="4" height="16"/>',
        'maximize': '<path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"/>',
        'minimize': '<path d="M8 3v3a2 2 0 0 1-2 2H3m18 0h-3a2 2 0 0 1-2-2V3m0 18v-3a2 2 0 0 1 2-2h3M3 16h3a2 2 0 0 1 2 2v3"/>',
        'check': '<polyline points="20 6 9 17 4 12"/>',
        'x': '<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>',
        'alert-triangle': '<path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>',
        'alert-circle': '<circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/>',
        'info': '<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>',
        'file': '<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/>',
        'file-text': '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/>',
        'code': '<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>',
        'image': '<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>',
        'video': '<polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/>',
        'music': '<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>',
        'film': '<rect x="2" y="2" width="20" height="20" rx="2.18" ry="2.18"/><line x1="7" y1="2" x2="7" y2="22"/><line x1="17" y1="2" x2="17" y2="22"/><line x1="2" y1="12" x2="22" y2="12"/><line x1="2" y1="7" x2="7" y2="7"/><line x1="2" y1="17" x2="7" y2="17"/><line x1="17" y1="17" x2="22" y2="17"/><line x1="17" y1="7" x2="22" y2="7"/>',
        'archive': '<polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5"/><line x1="10" y1="12" x2="14" y2="12"/>',
        'package': '<line x1="16.5" y1="9.4" x2="7.5" y2="4.21"/><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/>',
        'settings': '<circle cx="12" cy="12" r="3"/><path d="M12 1v6m0 6v6m5.66-13.66l-4.24 4.24m-2.83 2.83l-4.24 4.24m12.73 0l-4.24-4.24m-2.83-2.83L2.34 2.34"/>',
        'loader': '<line x1="12" y1="2" x2="12" y2="6"/><line x1="12" y1="18" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.76" y2="7.76"/><line x1="16.24" y1="16.24" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="6" y2="12"/><line x1="18" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.76" y2="16.24"/><line x1="16.24" y1="7.76" x2="19.07" y2="4.93"/>'
    };

    static createIcon(iconName, className = '', size = 20) {
        const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('fill', 'none');
        svg.setAttribute('stroke', 'currentColor');
        svg.setAttribute('stroke-width', '2');
        svg.setAttribute('stroke-linecap', 'round');
        svg.setAttribute('stroke-linejoin', 'round');
        svg.setAttribute('width', size);
        svg.setAttribute('height', size);
        if (className) svg.className = className;
        const paths = this.getIconPaths(iconName);
        if (paths) svg.innerHTML = paths;
        return svg;
    }

    static getIconHTML(iconName, className = '', size = 20) {
        const paths = this.getIconPaths(iconName);
        return `<svg class="${className}" viewBox="0 0 24 24" width="${size}" height="${size}" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${paths}</svg>`;
    }

    static getIconPaths(iconName) {
        return this.ICON_PATHS[iconName] || this.ICON_PATHS['file'];
    }
}

// ============================================================================
// FileTypeDetector - File type detection by extension and MIME
// ============================================================================

class FileTypeDetector {
    static FILE_TYPES = {
        text: ['txt', 'log', 'csv', 'tsv', 'ini', 'conf', 'cfg', 'env'],
        code: ['js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'go', 'mod', 'sum', 'py', 'pyw', 'pyc', 'pyd', 'pyo', 'html', 'htm', 'css', 'scss', 'sass', 'less', 'json', 'yaml', 'yml', 'toml', 'xml', 'sh', 'bash', 'zsh', 'fish', 'java', 'c', 'cpp', 'h', 'hpp', 'rs', 'rb', 'php', 'sql'],
        markdown: ['md', 'markdown', 'mdown', 'mkd'],
        image: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'bmp', 'ico', 'tiff', 'tif', 'avif'],
        video: ['mp4', 'webm', 'ogg', 'ogv', 'avi', 'mov', 'mkv', 'flv', 'wmv', 'm4v', '3gp'],
        audio: ['mp3', 'wav', 'ogg', 'oga', 'flac', 'm4a', 'aac', 'wma', 'opus', 'webm'],
        pdf: ['pdf'],
        archive: ['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'rar', '7z', 'cab', 'iso'],
        document: ['doc', 'docx', 'odt', 'rtf', 'xls', 'xlsx', 'ods', 'ppt', 'pptx', 'odp'],
        binary: ['exe', 'dll', 'so', 'dylib', 'bin', 'dat', 'o']
    };

    static MIME_TYPE_MAP = {
        'text/plain': 'text', 'text/csv': 'text', 'text/tab-separated-values': 'text',
        'text/javascript': 'code', 'application/javascript': 'code', 'application/json': 'code',
        'text/html': 'code', 'text/css': 'code', 'application/xml': 'code', 'text/xml': 'code',
        'text/markdown': 'markdown',
        'image/jpeg': 'image', 'image/png': 'image', 'image/gif': 'image', 'image/webp': 'image',
        'image/svg+xml': 'image', 'image/bmp': 'image', 'image/x-icon': 'image', 'image/avif': 'image',
        'video/mp4': 'video', 'video/webm': 'video', 'video/ogg': 'video', 'video/avi': 'video',
        'video/quicktime': 'video', 'video/x-msvideo': 'video', 'video/x-matroska': 'video',
        'audio/mpeg': 'audio', 'audio/wav': 'audio', 'audio/ogg': 'audio', 'audio/flac': 'audio',
        'audio/aac': 'audio', 'audio/mp4': 'audio', 'audio/webm': 'audio',
        'application/pdf': 'pdf',
        'application/zip': 'archive', 'application/x-tar': 'archive', 'application/gzip': 'archive',
        'application/x-rar-compressed': 'archive', 'application/x-7z-compressed': 'archive',
        'application/msword': 'document',
        'application/vnd.openxmlformats-officedocument.wordprocessingml.document': 'document',
        'application/vnd.ms-excel': 'document',
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': 'document',
        'application/octet-stream': 'binary'
    };

    static detect(filename, mimeType = null) {
        const extension = this.getExtension(filename);
        let category = this.detectByExtension(extension);
        if (!category && mimeType) category = this.detectByMimeType(mimeType);
        if (!category) category = 'binary';
        return {
            category,
            extension,
            isEditable: this.isEditable(category),
            isViewable: this.isViewable(category),
            canPreview: this.canPreview(category)
        };
    }

    static getExtension(filename) {
        if (!filename || typeof filename !== 'string') return '';
        const parts = filename.split('.');
        if (parts.length < 2) return '';
        return parts[parts.length - 1].toLowerCase();
    }

    static detectByExtension(extension) {
        if (!extension) return null;
        for (const [category, extensions] of Object.entries(this.FILE_TYPES)) {
            if (extensions.includes(extension)) return category;
        }
        return null;
    }

    static detectByMimeType(mimeType) {
        if (!mimeType) return null;
        if (this.MIME_TYPE_MAP[mimeType]) return this.MIME_TYPE_MAP[mimeType];
        const prefix = mimeType.split('/')[0];
        if (prefix === 'text') return 'text';
        if (prefix === 'image') return 'image';
        if (prefix === 'video') return 'video';
        if (prefix === 'audio') return 'audio';
        return null;
    }

    static isEditable(category) {
        return ['text', 'code', 'markdown'].includes(category);
    }

    static isViewable(category) {
        return !['binary', 'archive', 'document'].includes(category);
    }

    static canPreview(category) {
        return ['image', 'video', 'audio', 'pdf', 'markdown'].includes(category);
    }

    static getTypeName(category) {
        const names = {
            text: 'Text File', code: 'Source Code', markdown: 'Markdown',
            image: 'Image', video: 'Video', audio: 'Audio', pdf: 'PDF Document',
            archive: 'Archive', document: 'Document', binary: 'Binary File'
        };
        return names[category] || 'Unknown';
    }
}

// ============================================================================
// BaseFileViewer - Abstract base class for all file viewers
// ============================================================================

class BaseFileViewer {
    constructor(fileData) {
        if (this.constructor === BaseFileViewer) {
            throw new Error('BaseFileViewer is abstract and cannot be instantiated');
        }
        this.fileData = fileData;
        this.container = null;
        this.isDestroyed = false;
    }

    async render(container) {
        throw new Error('render() must be implemented by subclass');
    }

    canEdit() {
        return false;
    }

    getContent() {
        return null;
    }

    setContent(content) {
        throw new Error('This viewer does not support editing');
    }

    async save() {
        throw new Error('This viewer does not support saving');
    }

    destroy() {
        if (this.isDestroyed) return;
        if (this.container) {
            this.container.innerHTML = '';
            this.container = null;
        }
        this.isDestroyed = true;
    }

    getToolbarActions() {
        return [{
            label: 'Download',
            icon: IconHelper.getIconHTML('download', '', 16),
            action: () => this.download(),
            disabled: false
        }];
    }

    download() {
        const blob = this.createBlob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = this.fileData.name;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        URL.revokeObjectURL(url);
    }

    createBlob() {
        if (this.fileData.encoding === 'base64') {
            try {
                const binary = atob(this.fileData.content);
                const len = binary.length;
                const bytes = new Uint8Array(len);
                const chunkSize = 8192;
                for (let i = 0; i < len; i += chunkSize) {
                    const end = Math.min(i + chunkSize, len);
                    for (let j = i; j < end; j++) {
                        bytes[j] = binary.charCodeAt(j);
                    }
                }
                return new Blob([bytes], { type: this.fileData.mimeType });
            } catch (error) {
                console.error('Failed to decode base64:', error);
                throw new Error('Failed to decode file content');
            }
        } else {
            return new Blob([this.fileData.content], {
                type: this.fileData.mimeType || 'text/plain'
            });
        }
    }

    createDataURL() {
        // If streaming URL is provided (for large files), use it directly
        if (this.fileData.streamUrl) {
            return this.fileData.streamUrl;
        }

        if (this.fileData.encoding === 'base64') {
            return `data:${this.fileData.mimeType};base64,${this.fileData.content}`;
        } else {
            if (this.fileData.mimeType === 'image/svg+xml' || this.fileData.name?.endsWith('.svg')) {
                const svgContent = encodeURIComponent(this.fileData.content);
                return `data:image/svg+xml,${svgContent}`;
            }
            const blob = this.createBlob();
            return URL.createObjectURL(blob);
        }
    }

    formatFileSize(bytes) {
        if (bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
    }

    showError(message) {
        if (!this.container) return;
        const errorDiv = document.createElement('div');
        errorDiv.className = 'file-viewer-error';
        errorDiv.innerHTML = `
            <div class="error-icon">⚠️</div>
            <div class="error-message">${this.escapeHtml(message)}</div>
        `;
        this.container.appendChild(errorDiv);
    }

    // Optimized escapeHtml using regex (3-4x faster than DOM-based)
    escapeHtml(text) {
        if (typeof text !== 'string') return '';
        return text
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }
}

// ============================================================================
// FileViewerFactory - Factory for creating appropriate file viewers
// ============================================================================

class FileViewerFactory {
    static VIEWER_MAP = {
        text: 'TextViewer',
        code: 'TextViewer',
        markdown: 'TextViewer',
        image: 'ImageViewer',
        video: 'VideoViewer',
        audio: 'AudioViewer',
        pdf: 'PDFViewer'
    };

    static createViewer(fileData) {
        const fileType = FileTypeDetector.detect(fileData.name, fileData.mimeType);
        if (!fileType.isViewable) {
            return this.createDownloadViewer(fileData, fileType);
        }
        const viewerClassName = this.VIEWER_MAP[fileType.category];
        let viewer;
        try {
            switch (viewerClassName) {
                case 'TextViewer':
                    viewer = new TextViewer(fileData);
                    break;
                case 'ImageViewer':
                    viewer = new ImageViewer(fileData);
                    break;
                case 'VideoViewer':
                    viewer = new VideoViewer(fileData);
                    break;
                case 'AudioViewer':
                    viewer = new AudioViewer(fileData);
                    break;
                case 'PDFViewer':
                    viewer = new PDFViewer(fileData);
                    break;
                default:
                    viewer = this.createDownloadViewer(fileData, fileType);
            }
        } catch (error) {
            console.error('Failed to create viewer:', error);
            viewer = this.createDownloadViewer(fileData, fileType);
        }
        viewer.fileType = fileType;
        return viewer;
    }

    static createDownloadViewer(fileData, fileType) {
        return new DownloadViewer(fileData, fileType);
    }
}

// ============================================================================
// DownloadViewer - Fallback viewer for unsupported file types
// ============================================================================

class DownloadViewer extends BaseFileViewer {
    static ICON_MAP = {
        archive: 'archive', document: 'file-text', binary: 'settings',
        text: 'file-text', code: 'code', image: 'image',
        video: 'video', audio: 'music', pdf: 'file'
    };

    constructor(fileData, fileType) {
        super(fileData);
        this.fileType = fileType;
    }

    async render(container) {
        this.container = container;
        container.innerHTML = '';
        container.className = 'file-viewer-container download-viewer-container';

        const wrapper = document.createElement('div');
        wrapper.className = 'download-viewer-wrapper';

        const icon = document.createElement('div');
        icon.className = 'download-viewer-icon';
        icon.appendChild(this.getFileIcon());
        wrapper.appendChild(icon);

        const info = document.createElement('div');
        info.className = 'download-viewer-info';

        const fileName = document.createElement('div');
        fileName.className = 'download-viewer-filename';
        fileName.textContent = this.fileData.name;
        info.appendChild(fileName);

        const fileDetails = document.createElement('div');
        fileDetails.className = 'download-viewer-details';
        fileDetails.innerHTML = `
            <span>${FileTypeDetector.getTypeName(this.fileType.category)}</span>
            <span class="separator">•</span>
            <span>${this.formatFileSize(this.fileData.size)}</span>
        `;
        info.appendChild(fileDetails);

        const message = document.createElement('div');
        message.className = 'download-viewer-message';
        message.textContent = 'This file type cannot be previewed in the browser.';
        info.appendChild(message);

        wrapper.appendChild(info);

        const downloadBtn = document.createElement('button');
        downloadBtn.className = 'btn btn-primary download-viewer-button';
        downloadBtn.textContent = 'Download File';
        downloadBtn.onclick = () => this.download();
        wrapper.appendChild(downloadBtn);

        container.appendChild(wrapper);
    }

    getFileIcon() {
        const iconName = DownloadViewer.ICON_MAP[this.fileType.category] || 'file';
        return IconHelper.createIcon(iconName, '', 64);
    }

    getToolbarActions() {
        return [{
            label: 'Download',
            icon: IconHelper.getIconHTML('download', '', 16),
            action: () => this.download(),
            disabled: false,
            primary: true
        }];
    }
}

console.log('[FileManager] Core bundle loaded');
