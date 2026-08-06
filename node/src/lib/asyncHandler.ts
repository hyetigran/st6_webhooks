import type { NextFunction, Request, Response } from "express";

type AsyncRouteHandler = (req: Request, res: Response) => Promise<void>;

// Express 4 doesn't forward rejected promises from async handlers to the
// error middleware on its own — without this, a thrown error in a route
// leaves the request hanging instead of producing a 500.
export function asyncHandler(handler: AsyncRouteHandler) {
  return (req: Request, res: Response, next: NextFunction): void => {
    handler(req, res).catch(next);
  };
}
