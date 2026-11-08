export type SessionNotificationScheduler = (callback: () => void) => () => void;
export declare const scheduleSessionFrame: SessionNotificationScheduler;
export declare class SessionFrameNotifier {
    private readonly notify;
    private readonly schedule;
    private scheduled?;
    constructor(notify: () => void, schedule?: SessionNotificationScheduler);
    request(): void;
    flush(): void;
    cancel(): void;
}
