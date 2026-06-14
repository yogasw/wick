import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { AskRequest, AskAnswer } from "../types/agents.js";

export const getAsks = (base: string, id: string) =>
  apiGetE<{ pending: AskRequest[] }>(`${base}/sessions/${id}/asks`);

export const answerAsk = (base: string, id: string, body: AskAnswer) =>
  apiPostE<unknown>(`${base}/sessions/${id}/answer`, body);
