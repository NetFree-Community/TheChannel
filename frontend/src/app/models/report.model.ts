export interface Report {
    id: number;
    messageId: number;
    reason: string;
    createdAt: Date;
    updatedAt: Date;
    reporterId: string;
    reporterName: string;
    reportedEmail: string;
    closed: boolean;
}

export type Reports = Report[];