export interface Statistics {
    usersAmount: number;
    connectedUsersAmount: number;
    peakSSEConnections: PeakSSEConnections;
}

export interface PeakSSEConnections {
    value: number;
    timestamp: Date;
}
