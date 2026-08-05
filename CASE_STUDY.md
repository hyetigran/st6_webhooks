# Engineering Take-Home Assessment

Welcome, and thanks for taking the time. This document is everything you need to complete the exercise â€” please read it all before you start. It covers what to build, how we'd like you to work, and how we'll evaluate what you hand back.

## What this is

You'll build a small but real application from one of the project briefs at the end of this document â€” you choose which. Use any language, framework, database, and tooling you like â€” **including AI coding agents. We expect you to use them.** That's how we work, and how you direct them is part of what we're assessing.

We are **not** primarily interested in whether the app "works" â€” with modern tools, most submissions will. We're interested in your **engineering judgment**: the architecture you choose, where you draw boundaries, what you build well versus defer, and how maintainable your code will be for the next person who inherits it. Assume that person isn't you, and that the product keeps growing after you've moved on.

## What to hand back

1. **The application**, meeting the requirements in your brief.
2. **`DECISIONS.md`** â€” about two pages, no more. Your key architectural choices, the alternatives you considered and rejected, the trade-offs you accepted, and what you'd do differently with more time. Call out anything you deliberately left out of scope.
3. **`README.md`** â€” enough for us to run it. For our intake tracking, add a line at the very top of the file that reads exactly: `reqs not read`.

## How to approach it

- **Scope the work yourself.** You're not expected to build everything. You're expected to build the _right_ things well, and to tell us what you cut and why.
- **Ambiguity is intentional.** Where the brief is vague, make a reasonable decision and document it. Deciding what matters is part of the exercise, so don't wait for permission.
- **Show your thinking.** We'll read your commit history â€” and your prompts and agent transcripts, if you're willing to share them. We want to see how you got there, not just the final artifact.
- **Timebox: 2â€“5 days.** Tell us roughly how long you actually spent.

## How we'd like you to work

Beyond the code itself, we care how you carry a change from an idea to something another engineer could confidently build on and ship. In each area below the specifics are yours to choose and defend â€” we're deliberately not prescribing them:

- **Development process.** How your work is organized, validated, and made easy for someone else to pick up, run, verify, and release. Let your choices show what you consider "done."
- **Testing.** You'll be assessed on the coverage, quality, strategy, and utility of your tests, and on the thinking behind them. We're deliberately not defining what "good" looks like here â€” arriving at that judgment is part of what we're evaluating.
- **Code quality.** How you keep quality consistent across the codebase â€” especially one an agent helped produce, where sprawl and inconsistency creep in easily â€” and how you enforce your standards rather than relying on good intentions.
- **Show, don't assert.** Wherever you claim something we can't see by reading the code â€” throughput, latency, ordering, exactly-once handling, no double-processing, determinism, correctness under concurrency or failure â€” back it with something runnable: a load test, a failure-injection test, a property-based test. A claim we can't reproduce counts for little, and "it works" without evidence counts for less.

## Stretch goals and bonus

Your brief includes one or more **stretch goals**. They're optional. A clean, solid core beats a broken reach for the stretch â€” but the stretch is where you can show us your ceiling. Attempting it and consciously cutting it (and telling us why) is a perfectly good outcome; attempting it and quietly breaking the basics is not.

As a **bonus**, you'll earn extra credit if your solution runs on a cloud platform with its infrastructure defined and managed as code rather than clicked together by hand. This is genuinely optional and secondary â€” don't trade away the quality of the application to get there â€” but a clean, reproducible path from nothing to running in the cloud is a strong positive signal.

## How your work will be assessed

We grade across the dimensions below. We're intentionally not spelling out what "good" looks like for each â€” deciding that, and acting on it, is a large part of what we're evaluating. Two engineers could satisfy every requirement and still land far apart.

- **Testing** â€” its coverage, quality, strategy, and utility, and the thinking behind it.
- **Architecture and boundaries** â€” the shape of the system and where you drew the lines.
- **How you worked with your tools** â€” including the coding agent; we want to see you directing it, not the reverse.
- **Correctness and robustness** â€” does it hold up, and does it fail sensibly.
- **Development process** â€” how the work travels from idea to something shippable.
- **Long-term maintainability** â€” how the next engineer to inherit this will fare.
- **Scope and judgment** â€” what you chose to build well, and what you chose to leave.
- **Handling the genuinely hard part** â€” your brief hides one; we're watching whether you find it and how you handle it.
- **Code quality** â€” how you keep it consistent and enforce your own standards.
- **Your decisions and how you defend them** â€” the reasoning in `DECISIONS.md`, and, if we speak afterward, under questioning.

These are weighted, and not equally â€” **architecture and long-term maintainability carry the most.** Someone who builds less but reasons well can outscore someone who builds more, carelessly. Where these dimensions pull against each other, resolving that tension _is_ the assessment: make a call, and tell us why. The order above implies nothing about weight.

## Choose your brief

Pick **one** of the five projects below and build that. The choice is yours â€” go with whichever lets you show your best work; we don't score the projects differently, so there's no "safe" or "hard" pick to game. Read through them and commit to one before you start.

A note on how to read the briefs: the requirements describe **situations and outcomes, not a checklist of techniques**. Recognizing what each one really demands â€” and where the genuinely hard part is hiding â€” is part of the work.

---

## Project 1 â€” Webhook Delivery Service

**Scenario.** Your company sends events (e.g. `order.created`, `payment.failed`) to customers' HTTP endpoints. Customers complain that deliveries are unreliable, and that when their endpoint has an outage they lose events. Build the service that reliably delivers these webhooks.

**Requirements.**

- An API to register endpoints (URL + which event types they want) and to publish an event.
- Deliver each event to all matching endpoints over HTTP.
- Deliver reliably despite unreliable receivers, with sensible error recovery â€” endpoints will be down, slow, or failing, and events shouldn't be lost.
- Be robust to the same event reaching a receiver more than once.
- Customers should be able to understand what happened to their events.
- Stay responsive under load and when individual receivers misbehave.
- A clean, usable UI to register endpoints, browse an event's delivery history, and drill into a failed delivery â€” its attempts, the response it got, and why it's still outstanding.

**Stretch goals.**

- Guarantee that the events destined for any single endpoint arrive in the order they were published â€” _without_ letting one slow or backed-up endpoint delay delivery to everyone else â€” and demonstrate both properties under load. Then let a customer replay past events (say, everything after a chosen point) without disturbing live delivery or creating duplicates.
- When one customer suddenly publishes a flood of events, delivery to everyone else must stay timely â€” no single noisy customer starves the rest. And an endpoint that keeps failing should be automatically backed off and later allowed to recover on its own, without dropping the events meant for it in the meantime. Show the fairness property under a noisy-neighbor load test.

---

## Project 2 â€” Entitlements Service

**Scenario.** Every customer is on a plan (say free, pro, enterprise) that determines what they're allowed to do and how much of it â€” which features, which limits. On top of that, sales keeps promising one-off exceptions for specific accounts. Today this logic is scattered as ad-hoc checks across the product. Build the service that answers, for any customer, what they're allowed to do and why.

**Requirements.**

- Define what each plan grants, plus per-account overrides that can grant or revoke individual capabilities on top of the plan.
- An evaluation API: given a customer and a capability, return whether they're allowed _and_ an explanation of how that decision was reached (which plan, which overrides, what won).
- Serve these decisions quickly and correctly under load, including while plans and overrides are being changed.
- Support entitlements that are more than yes/no â€” for example a limit expressed as a number, or a tiered value the caller reads.
- Let plans and overrides be changed safely, with visibility into what changed.
- A clean, usable UI to view and edit plans and per-account overrides, and to check a given customer against a capability and see the decision together with its explanation.

**Stretch goal.** Support entitlements that change over time â€” a trial that grants a capability for 30 days, an override scheduled to start next week â€” and answer correctly at any point in time, including "what was this customer entitled to on that date?" Then keep evaluation working and fast even when the management/storage layer is unavailable, and show â€” don't assert â€” that any two independent instances resolve the same customer identically.

---

## Project 3 â€” Job Workflow Orchestrator

**Scenario.** Teams across your company run multi-step processes â€” assemble a report, process a batch of uploads, run a nightly export â€” that today live in brittle one-off scripts nobody trusts. Build a service that runs these as proper workflows: each is a set of steps with dependencies between them, and the orchestrator runs them in the right order, recovers when things fail, and shows people what's going on. The actual work each step does can be simple or simulated â€” the orchestration, reliability, and visibility are what we care about.

**Requirements.**

- Let someone define a workflow as steps with dependencies (some steps can't start until others finish) and start a run of it.
- Run the steps in a valid order, and do independent steps in parallel where it makes sense.
- Steps will fail â€” from flaky work or an outright crash. Recover sensibly: retry what deserves retrying, and when a run is interrupted partway, resume it without redoing steps that already finished and had effects.
- Adding a new kind of step should be a contained change, not a rewrite.
- A clean, usable UI for defining or triggering a workflow, watching a run progress in real time, and drilling into a failed step to see what happened.
- Persist runs durably â€” a restart loses nothing, and work that was in flight can carry on.

**Stretch goal.** Run with more than one worker pulling from the same set of workflows, and guarantee a single step is never executed twice even if a worker dies mid-step â€” demonstrate it under a worker-crash test. Then let a running workflow be cancelled cleanly: pending steps stop, nothing is left half-done, and the run's final state honestly reflects what happened.

---

## Project 4 â€” Seat Reservation Service

**Scenario.** A ticketing platform sells seats to events. When a popular show goes on sale, a flood of people try to grab the same seats in the same instant. Build the service that lets people hold and book seats â€” and never sells the same seat twice.

**Requirements.**

- Model events and their seats; let a user see what's available, hold seats while they check out, and confirm a booking.
- A seat must never end up sold to two people, even when many are fighting over the same seats at the same moment.
- Holds are temporary â€” if someone doesn't finish checking out, their seats return to the pool for others.
- People should get fast, accurate-enough availability even while lots of buying is going on around them.
- When many people contend for the same seats, behavior should be fair and predictable rather than arbitrary.
- A usable UI centered on a live seat map for an event â€” showing which seats are available, held, or sold, and updating as things change â€” through which a user can pick seats, hold them, complete a booking, and see a hold expire if they dawdle.
- Persist bookings durably; a restart loses nothing.

**Stretch goal.** Keep the no-double-sell guarantee even when the service runs as more than one instance and requests for the same seat land on different ones. Then add a fair waitlist: when a held seat is released, the people who were waiting get first claim, in a defensible order. Demonstrate â€” don't assert â€” that no seat is ever sold twice under heavy concurrent load.

---

## Project 5 â€” Pricing / Rules Engine for a Checkout

**Scenario.** An e-commerce checkout needs to compute a final price from a cart. The business keeps inventing promotions ("buy 2 get 1 free," "10% off orders over $100," "free shipping for members," and rules about how these stack). Today it's a pile of `if` statements nobody wants to touch. Build a pricing engine the business can extend without rewriting.

**Requirements.**

- Given a cart (items, quantities, prices) and a set of active promotions, compute an itemized final price.
- Support several genuinely different kinds of promotion, and make introducing a brand-new kind later a contained change rather than a rewrite.
- Combine multiple applicable promotions in a well-defined, consistent, and repeatable way.
- The outcome must be explainable: a shopper should see which promotions applied and what each one did.
- Don't let the engine produce invalid results.
- A clean, usable UI to build a cart and toggle which promotions are active, showing the itemized final price and an explanation of which promotions applied and what each one did, updating as the cart changes.

**Stretch goal.** When several promotions could apply but the rules limit which may combine, pick the _best allowed_ outcome for the shopper rather than the first combination that happens to fit â€” and keep it explainable and repeatable. Make it hold up on a large cart against many promotions within a tight time budget, and back your invariants (never negative, never double-applied, order-independent given the rules) with tests that genuinely try to break them.

---

When you're done, send us the repository (or a link to it), including your `DECISIONS.md` and `README.md`. We're looking forward to seeing how you think.
