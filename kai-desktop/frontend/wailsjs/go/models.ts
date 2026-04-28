export namespace main {
	
	export class AgentDTO {
	    name: string;
	    color: string;
	    path: string;
	    workspace: string;
	    sync_mode: string;
	    source_repo?: string;
	    checkpoints: number;
	    uptime_sec: number;
	    last_file?: string;
	    last_event_ts?: number;
	    sparkline: number[];
	
	    static createFrom(source: any = {}) {
	        return new AgentDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.color = source["color"];
	        this.path = source["path"];
	        this.workspace = source["workspace"];
	        this.sync_mode = source["sync_mode"];
	        this.source_repo = source["source_repo"];
	        this.checkpoints = source["checkpoints"];
	        this.uptime_sec = source["uptime_sec"];
	        this.last_file = source["last_file"];
	        this.last_event_ts = source["last_event_ts"];
	        this.sparkline = source["sparkline"];
	    }
	}
	export class EventDTO {
	    type: string;
	    agent: string;
	    agent_name: string;
	    color: string;
	    file?: string;
	    timestamp: number;
	    detail?: string;
	
	    static createFrom(source: any = {}) {
	        return new EventDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.agent = source["agent"];
	        this.agent_name = source["agent_name"];
	        this.color = source["color"];
	        this.file = source["file"];
	        this.timestamp = source["timestamp"];
	        this.detail = source["detail"];
	    }
	}
	export class HeaderDTO {
	    agent_count: number;
	    repo_count: number;
	    repos: string[];
	    sole_repo?: string;
	
	    static createFrom(source: any = {}) {
	        return new HeaderDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agent_count = source["agent_count"];
	        this.repo_count = source["repo_count"];
	        this.repos = source["repos"];
	        this.sole_repo = source["sole_repo"];
	    }
	}

}

