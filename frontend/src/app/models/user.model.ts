export interface User {
    id: string;
    username: string;
    picture: string;
    privileges: Record<string, boolean>;
    email: string;
}