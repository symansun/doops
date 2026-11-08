import { type ReactNode } from "react";
import { WorkerClient } from "../WorkerClient.js";
import type { WorkerTransport } from "../contracts.js";
export declare function WorkerProvider({ children, client, transports, }: {
    children: ReactNode;
    client?: WorkerClient;
    transports?: readonly WorkerTransport[];
}): import("react/jsx-runtime").JSX.Element;
export declare function useWorkerClient(): WorkerClient;
