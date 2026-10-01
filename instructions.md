Project Initialization & Operational Directive for Antigravity
> CRITICAL INITIALIZATION INSTRUCTION: When this file is provided at startup, do not jump straight into coding. You must follow the interactive bootstrap sequence below step-by-step before touching any source code.
> 
Phase 1: Big Picture & Multiple-Choice Discovery
 * Acknowledge and Prompt: Read this file, then immediately ask the user to provide a few sentences describing the big picture of the project (what it is and what core problem it solves).
 * Interactive Multiple-Choice Q&A: Based on the user's description, generate a series of high-level architectural and product questions.
   * Every question must be formatted as a multiple-choice list with numbered options (e.g., 1, 2, 3).
   * The final option must always be an open-ended write-in option allowing the user to type a custom answer.
   * ask each question one at a time and make it so the user can hit the corasponding number to answer and then automaticly move on. Wait for the user's responses before moving forward.
 * Requirement Logging: Initialize and maintain a local tracking document (PROJECT_STATE.md) containing project requirements, current constraints, and a running changelog of progress. This file must be updated continuously as work proceeds.
Phase 2: Competitor & Open-Source Research
 * Ecosystem Scan: Systematically research potential commercial competitors and existing open-source projects tackling similar problems.
 * Differentiation Analysis: Present a brief summary of findings to the user and ask targeted questions:
   * What features or behaviors should be kept the same as existing solutions?
   * What specific pain points should be done differently or better?
Phase 3: UI & Design Assets Check
 * Asset Query: Ask the user if they have specific UI/UX design ideas, wireframes, or mockup images.
   * If yes, instruct the user to drop the image files into the designated local folder (/assets/ui/ or similar).
   * If no, propose a clean, functional design approach and proceed with what you think is best without defaulting to any legacy templates.
Phase 4: Local-First Execution & Deployment Rules
All projects spawned under this template must strictly adhere to these operational and infrastructure rules:
 * Always Run Locally First: All development, building, and testing must be executed and verified locally first. You are strictly forbidden from pushing updates, deploying live, or pushing to GitHub/stiqrhub without explicit, direct permission from the user.
 * Live Version Tagging: Anytime a version is approved and pushed live, it must be explicitly marked with a formal version number (Semantic Versioning: vMAJOR.MINOR.PATCH).
 * Build Archiving: All finished production outputs (e.g., .apk files, web build zips, standalone python scripts, etc.) must be saved and tracked so that version history is safely preserved for rollbacks.
 * GitHub Ready (stiqrhub): The repository structure must be cleanly organized, .gitignore-compliant, and staged ready for push to stiqrhub only upon user command.
 * Settings Update Card Integration: Every application must include a dedicated update card component in its Settings view that queries version history, checks if a newer release is available, displays update status, and allows direct downloading right from the UI.
