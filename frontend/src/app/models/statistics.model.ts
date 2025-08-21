export interface Statistics {
    usersAmount: number;
    connectedUsersAmount: number;
    peakSSEConnections: PeakSSEConnections;
    connectionsStatistics: ConnectionsStatistics;
}

export interface PeakSSEConnections {
    value: number;
    timestamp: Date;
}

export interface ConnectionsStatistics {
    date: number[];
    labels: string[];
}
